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

func TestPerformStableKeyIncludesPathParamsAnd202IsAccepted(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(202)
	}))
	defer srv.Close()
	for _, project := range []string{"7", "8"} {
		r := testRunner(t, srv, t.TempDir())
		_, err := r.Perform(context.Background(), "advance", "projects:projectStateAdvance", map[string]string{"projectId": project}, nil, nil, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("keys %v", keys)
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
