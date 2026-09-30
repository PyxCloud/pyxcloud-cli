package passocli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testRunner(t *testing.T, srv *httptest.Server, dir string) *Runtime {
	t.Helper()
	p, _ := passoauth.ResolveProfile("sandbox")
	return &Runtime{Profile: p, Client: passotransport.New(srv.URL, nil), LedgerPath: dir + "/ledger.json", EvidenceDir: dir + "/evidence", ProjectID: 42, timeout: time.Second, now: time.Now}
}

func TestPerformPendingBeforeRequestAndAlwaysInvokesServer(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		ledger, err := passostate.Load(dir + "/ledger.json")
		if err != nil || ledger.Operations["projects:projectStateAdvance"].State != "pending" {
			t.Errorf("request preceded pending ledger: %#v %v", ledger, err)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["idempotencyKey"] == nil || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("missing managed idempotency: %#v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":{"projectId":42,"versionId":"v-1"}}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, dir)
	for i := 0; i < 2; i++ {
		got, err := r.Perform(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, url.Values{}, json.RawMessage(`{"state":"ready"}`), true)
		if err != nil || got.Status != "accepted" || got.Stage != "advance" || got.VersionID != "v-1" {
			t.Fatalf("got %#v err %v", got, err)
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d; retries must reach server", calls)
	}
	ledger, _ := passostate.Load(r.LedgerPath)
	if ledger.Operations["projects:projectStateAdvance"].State != "accepted" {
		t.Fatalf("ledger %#v", ledger)
	}
}

func TestFreezeResponsePersistsUUIDAndVersionSequenceSeparately(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/42/contract/release-freeze" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":{"versionId":"9fce9406-6e8e-4e74-9342-2d3c7ba4207e","versionLabel":"r1-6387061","versionSequence":23,"releaseId":"rel-1"}}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, dir)
	got, err := r.Perform(context.Background(), "release", "journeycontract:releaseFreezeCreate", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{"expectedVersion":4}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionID != "9fce9406-6e8e-4e74-9342-2d3c7ba4207e" || got.VersionLabel != "r1-6387061" || got.VersionSequence != 23 || r.VersionSequence != 23 {
		t.Fatalf("freeze identities not observed separately: result=%#v runtime=%#v", got, r)
	}
	data, err := os.ReadFile(r.LedgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var ledger map[string]any
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger["versionId"] != "9fce9406-6e8e-4e74-9342-2d3c7ba4207e" || ledger["versionLabel"] != "r1-6387061" || ledger["versionSequence"] != float64(23) {
		t.Fatalf("ledger must preserve both identities: %s", data)
	}
}

func TestFreezeLockCapturesAuthoritativeLabelAndRejectsUnsafeOrMismatchedLabels(t *testing.T) {
	tests := []struct {
		name, body string
		wantErr    string
	}{
		{"lock label", `{"data":{"versionId":"9fce9406-6e8e-4e74-9342-2d3c7ba4207e","versionLabel":"r2-6387061"}}`, ""},
		{"unsafe label", `{"data":{"versionId":"9fce9406-6e8e-4e74-9342-2d3c7ba4207e","versionLabel":"../main"}}`, "invalid_version_label"},
		{"label without version UUID", `{"data":{"versionLabel":"r2-6387061"}}`, "invalid_version_label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			r := testRunner(t, srv, t.TempDir())
			_, err := r.Perform(context.Background(), "release", "journeycontract:releaseVersionLockCreate", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{}`), false)
			if tt.wantErr == "" {
				if err != nil || r.VersionLabel != "r2-6387061" {
					t.Fatalf("lock label=%q err=%v", r.VersionLabel, err)
				}
				return
			}
			var exit *ExitError
			if !errors.As(err, &exit) || exit.Code != tt.wantErr {
				t.Fatalf("err=%v, want %s", err, tt.wantErr)
			}
		})
	}
}

func TestArbitraryResponseCannotSetVersionLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"versionId":"9fce9406-6e8e-4e74-9342-2d3c7ba4207e","versionLabel":"r9-abcdef0","version":"do-not-use"}}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, t.TempDir())
	_, err := r.Perform(context.Background(), "release", "journeycontract:releaseEligibilityRead", map[string]string{"projectId": "42"}, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.VersionLabel != "" {
		t.Fatalf("non-authoritative response set version label %q", r.VersionLabel)
	}
}

func TestReleaseBranchRoutesCannotEscapeRuntimeVersionLabel(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, t.TempDir())
	r.VersionLabel = "r1-6387061"
	_, err := r.Perform(context.Background(), "release", "journeycontract:releaseBranchesPreview", map[string]string{"projectId": "42"}, url.Values{"version": []string{"r2-6387061"}}, nil, false)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != "scope_mismatch" || calls != 0 {
		t.Fatalf("preview err=%v calls=%d", err, calls)
	}
	_, err = r.Perform(context.Background(), "release", "journeycontract:releaseBranchesPreview", map[string]string{"projectId": "42"}, url.Values{"version": []string{"r1-6387061", "r2-6387061"}}, nil, false)
	if !errors.As(err, &exit) || exit.Code != "scope_mismatch" || calls != 0 {
		t.Fatalf("duplicate preview versions err=%v calls=%d", err, calls)
	}
	_, err = r.Perform(context.Background(), "release", "journeycontract:releaseBranchesMaterialize", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{"version":"r2-6387061"}`), false)
	if !errors.As(err, &exit) || exit.Code != "scope_mismatch" || calls != 0 {
		t.Fatalf("materialize err=%v calls=%d", err, calls)
	}
}

func TestCloudScopeUsesVersionSequenceAndRejectsUUIDOrdinalGuess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/vibe/projects/42/versions/23/cloud/compare" {
			t.Errorf("cloud path=%q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":{"versionId":23}}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, t.TempDir())
	r.VersionID = "9fce9406-6e8e-4e74-9342-2d3c7ba4207e"
	r.VersionSequence = 23
	_, err := r.Perform(context.Background(), "cloud", "regioncompare.v2:getCloudCompareV2", map[string]string{"projectId": "42", "versionId": "23"}, nil, nil, false)
	if err != nil {
		t.Fatalf("authoritative sequence should match cloud route: %v", err)
	}
	if r.VersionID != "9fce9406-6e8e-4e74-9342-2d3c7ba4207e" {
		t.Fatalf("numeric cloud versionId replaced UUID: %q", r.VersionID)
	}
	_, err = r.Perform(context.Background(), "cloud", "regioncompare.v2:getCloudCompareV2", map[string]string{"projectId": "42", "versionId": r.VersionID}, nil, nil, false)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != "scope_mismatch" || calls != 1 {
		t.Fatalf("UUID must not be guessed as ordinal: err=%v calls=%d", err, calls)
	}
}

func TestPerformStableKeyIncludesPathParamsAnd202IsAccepted(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(202)
	}))
	defer srv.Close()
	for _, queryValue := range []string{"first", "second"} {
		r := testRunner(t, srv, t.TempDir())
		_, err := r.Perform(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, url.Values{"mode": {queryValue}}, nil, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("keys %v", keys)
	}
}

func TestPerformNestedObjectKeyOrderHasStableIdempotency(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	r := testRunner(t, srv, t.TempDir())
	for _, body := range []string{`{"outer":{"a":1,"b":{"c":2,"d":3}}}`, `{"outer":{"b":{"d":3,"c":2},"a":1}}`} {
		if _, err := r.Perform(context.Background(), "write", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, json.RawMessage(body), false); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("nested JSON key order changed idempotency key: %v", keys)
	}
}

func TestPerformRejectsScopeMismatchBeforeLedgerOrNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.WriteString(w, `{}`) }))
	defer srv.Close()
	dir := t.TempDir()
	r := testRunner(t, srv, dir)
	r.VersionID = "v-current"
	cases := []struct {
		operation string
		params    map[string]string
	}{
		{"projects:projectStateRead", map[string]string{"projectId": "43"}},
		{"securitygate:getSecurityGateEvaluation", map[string]string{"projectId": "42", "versionId": "v-other"}},
		{"vibe-docs-boardos:listDecisions", map[string]string{"id": "43"}},
	}
	for _, tc := range cases {
		_, err := r.Perform(context.Background(), "read", tc.operation, tc.params, nil, nil, false)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != "scope_mismatch" {
			t.Fatalf("%s returned %v", tc.operation, err)
		}
	}
	if calls != 0 {
		t.Fatalf("made %d requests despite scope mismatch", calls)
	}
	if _, err := os.Stat(r.LedgerPath); !os.IsNotExist(err) {
		t.Fatalf("scope mismatch created ledger: %v", err)
	}
}

func TestPerformClassifiesHTTPMutationFailuresByCertainty(t *testing.T) {
	for _, tc := range []struct {
		status   int
		state    string
		evidence string
		exitCode int
	}{{http.StatusServiceUnavailable, "uncertain", "uncertain", 30}, {http.StatusRequestTimeout, "uncertain", "uncertain", 20}, {http.StatusBadRequest, "failed", "failed", 20}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			dir := t.TempDir()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); _, _ = io.WriteString(w, `{}`) }))
			defer srv.Close()
			r := testRunner(t, srv, dir)
			_, err := r.Perform(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, nil, false)
			if got := ExitCode(err); got != tc.exitCode {
				t.Fatalf("ExitCode(%v)=%d, want %d", err, got, tc.exitCode)
			}
			ledger, loadErr := passostate.Load(r.LedgerPath)
			if loadErr != nil || ledger.Operations["projects:projectStateAdvance"].State != tc.state {
				t.Fatalf("ledger state=%q err=%v", ledger.Operations["projects:projectStateAdvance"].State, loadErr)
			}
			entries, readErr := os.ReadDir(r.EvidenceDir)
			if readErr != nil || len(entries) != 1 {
				t.Fatalf("evidence entries=%v err=%v", entries, readErr)
			}
			var evidence passostate.Evidence
			data, readErr := os.ReadFile(r.EvidenceDir + "/" + entries[0].Name())
			if readErr != nil || json.Unmarshal(data, &evidence) != nil || evidence.Status != tc.evidence {
				t.Fatalf("evidence status=%q err=%v data=%s", evidence.Status, readErr, data)
			}
		})
	}
}

func TestPerformRejectsMismatchedProjectAndMalformedResponse(t *testing.T) {
	for _, response := range []string{`{"data":{"projectId":43}}`, `not-json`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, response) }))
		r := testRunner(t, srv, t.TempDir())
		_, err := r.Perform(context.Background(), "read", "projects:projectStateRead", map[string]string{"projectId": "42"}, nil, nil, false)
		srv.Close()
		if err == nil {
			t.Fatalf("response %q accepted", response)
		}
	}
}

func TestPerformAllowsArrayResponseAndPersistsObservedIDs(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/projects") {
			_, _ = io.WriteString(w, `[{"id":1},{"id":2}]`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"projectId":42,"versionId":"v-2"}}`)
	}))
	defer srv.Close()
	r := testRunner(t, srv, dir)
	if got, err := r.Perform(context.Background(), "list", "projects:projectList", nil, nil, nil, false); err != nil || !json.Valid(got.Data) {
		t.Fatalf("array response got %#v, err %v", got, err)
	}
	if _, err := r.Perform(context.Background(), "read", "projects:projectStateRead", map[string]string{"projectId": "42"}, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	ledger, err := passostate.Load(r.LedgerPath)
	if err != nil || ledger.VersionID != "v-2" || len(ledger.Operations) != 0 {
		t.Fatalf("ledger %#v err %v", ledger, err)
	}
}

func TestPerformDefaultsZeroTimeoutAndReportsEvidenceFailure(t *testing.T) {
	dir := t.TempDir()
	evidenceBlocker := dir + "/not-a-directory"
	if err := os.WriteFile(evidenceBlocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	sawDefaultTimeout := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `[]`) }))
	defer srv.Close()
	r := testRunner(t, srv, dir)
	r.Client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		remaining := time.Until(deadline)
		sawDefaultTimeout = ok && remaining > time.Minute
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`)), Request: req}, nil
	})}
	r.timeout = 0
	r.EvidenceDir = evidenceBlocker + "/evidence"
	_, err := r.Perform(context.Background(), "list", "projects:projectList", nil, nil, nil, false)
	var exit *ExitError
	if !sawDefaultTimeout || !errors.As(err, &exit) || exit.Code != "evidence_write_failed" {
		t.Fatalf("default timeout=%v err=%v", sawDefaultTimeout, err)
	}
}

func TestReadInputBoundsAndRejectsTrailingJSON(t *testing.T) {
	if _, err := ReadInput("-", strings.NewReader(`{} {}`)); err == nil {
		t.Fatal("accepted trailing JSON")
	}
	if _, err := ReadInput("-", strings.NewReader("{\"x\":\""+strings.Repeat("a", maxInputBytes)+"\"}")); err == nil {
		t.Fatal("accepted oversized input")
	}
	path := t.TempDir() + "/input.json"
	if err := os.WriteFile(path, []byte(`{"x":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadInput(path, nil); err != nil || string(got) != `{"x":1}` {
		t.Fatalf("got %s err %v", got, err)
	}
}

func TestPerformEvidenceContainsNoRequestPayload(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer srv.Close()
	r := testRunner(t, srv, dir)
	_, err := r.Perform(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{"secret":"do-not-store"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(r.EvidenceDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries %v err %v", entries, err)
	}
	data, err := os.ReadFile(r.EvidenceDir + "/" + entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-store") {
		t.Fatalf("payload leaked in evidence: %s", data)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0600 {
		t.Fatalf("evidence mode %o", info.Mode().Perm())
	}
}

func TestPerformWithIfMatchFingerprintsVersionAndNeverAddsBodyField(t *testing.T) {
	var keys, matches, bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		matches = append(matches, r.Header.Get("If-Match"))
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	for _, version := range []int64{7, 8} {
		r := testRunner(t, server, t.TempDir())
		if _, err := r.PerformWithIfMatch(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{"state":"ready"}`), false, &version); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 || keys[0] == keys[1] || matches[0] != "7" || matches[1] != "8" {
		t.Fatalf("keys=%q If-Match=%q", keys, matches)
	}
	for _, body := range bodies {
		if body != `{"state":"ready"}` || strings.Contains(body, "If-Match") {
			t.Fatalf("If-Match entered request body: %s", body)
		}
	}
}

func TestPerformWithIfMatchRejectsNegativeBeforeLedgerAndNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	dir := t.TempDir()
	r := testRunner(t, server, dir)
	version := int64(-1)
	_, err := r.PerformWithIfMatch(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, json.RawMessage(`{"state":"ready"}`), false, &version)
	if err == nil || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
	if _, statErr := os.Stat(r.LedgerPath); !os.IsNotExist(statErr) {
		t.Fatalf("negative version wrote ledger: %v", statErr)
	}
}

func TestPerformChecksDocumentationProjectVersionAgainstSelectedUUID(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	for _, selected := range []string{"selected-uuid", ""} {
		r := testRunner(t, server, t.TempDir())
		r.VersionID = selected
		_, err := r.Perform(context.Background(), "docs", "documentation:documentationSnapshotRead", map[string]string{"projectId": "42", "projectVersionId": "other-uuid"}, nil, nil, false)
		if err == nil || err.Error() != "scope_mismatch" || calls != 0 {
			t.Fatalf("selected=%q error=%v calls=%d", selected, err, calls)
		}
	}
}
