package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/api/gen/journey"
	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// journeyResponseFixture mirrors the backend journey read contract:
// apicontract.Envelope[journeyreadcontract.JourneyRead]
// (platform/pyx-backend go/internal/apicontract/envelope.go,
// go/internal/journeyreadcontract/dto.go; vendored as
// api/contracts/journey.openapi.json). The envelope version is numeric
// (apicontract.Version int64, JourneyEnvelope.version int64).
const journeyResponseFixture = `{
  "data": {
    "projectId": 42,
    "version": {"id":"v-7","label":"0.1.0","sequence":7,"lockedAt":"2026-09-29T10:00:00Z"},
    "stage": "deploy",
    "subState": "deploy.in_progress",
    "primaryAction": {"key":"deploy.retry","label":"Retry deploy","href":"/projects/42/deploy","allowed":true},
    "needsYou": [],
    "facts": {"architecture": {"components":["api"]}},
    "projection": {"macro":"in_progress","micro":"build 3/5"}
  },
  "capabilities": [],
  "blockers": [],
  "freshness": {"asOf":"2026-09-29T11:00:00Z"},
  "version": 1
}`

// stubJourneyServer serves the fixture (or 401) as the journey endpoint would.
func stubJourneyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vibe/projects/42/journey" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing bearer token, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestPassoStatusJSONShape proves `passo status --json` emits journey +
// nextAction with exactly the journeycontract field names, and exit code 0
// when the next action is allowed.
func TestPassoStatusJSONShape(t *testing.T) {
	config.SetPathForTests(t.TempDir())
	resetProfile := profile
	profile = ""
	defer func() { profile = resetProfile }()

	if err := config.SaveProfile("", &config.Config{Token: "jwt-token", APIURL: "stub"}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	srv := stubJourneyServer(t, http.StatusOK, journeyResponseFixture)
	defer srv.Close()

	root := rootCmd
	root.SetArgs([]string{"passo", "status", "-p", "42", "--json", "--api-url", srv.URL})
	out := &strings.Builder{}
	root.SetOut(out)
	root.SetErr(out)
	t.Cleanup(func() { root.SetArgs(nil); root.SetOut(nil); root.SetErr(nil) })

	err := root.Execute()
	var ec ExitCoder
	if err != nil && !errorsAs(err, &ec) {
		t.Fatalf("execute: %v", err)
	}
	if err != nil && ec.ExitCode() != ExitOK {
		t.Fatalf("exit code = %d, want 0 (%v)", ec.ExitCode(), err)
	}

	var payload struct {
		Journey    journey.JourneyRead   `json:"journey"`
		NextAction journey.PrimaryAction `json:"nextAction"`
	}
	if err := json.Unmarshal([]byte(out.String()), &payload); err != nil {
		t.Fatalf("decode --json output: %v\noutput:\n%s", err, out.String())
	}
	if deref(payload.Journey.Stage) != "deploy" || deref(payload.Journey.SubState) != "deploy.in_progress" {
		t.Errorf("journey stage/subState = %q/%q", deref(payload.Journey.Stage), deref(payload.Journey.SubState))
	}
	if deref(payload.Journey.ProjectId) != 42 || payload.Journey.Version == nil || deref(payload.Journey.Version.Sequence) != 7 {
		t.Errorf("journey project/version mismatch: %+v", payload.Journey.Version)
	}
	if deref(payload.NextAction.Key) != "deploy.retry" || !deref(payload.NextAction.Allowed) {
		t.Errorf("nextAction = %+v", payload.NextAction)
	}
	// Contract field names must be preserved verbatim (no invented fields):
	// the raw --json output must carry the envelope's data fields.
	for _, want := range []string{"\"projectId\": 42", "\"primaryAction\"", "\"subState\": \"deploy.in_progress\"", "\"needsYou\": []"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing contract fragment %s", want)
		}
	}
}

// TestPassoStatusExitCodes wires the exit-code contract: 10 when the next
// action is not allowed, 20 on HTTP 401, 30 on a broken contract response.
func TestPassoStatusExitCodes(t *testing.T) {
	resetProfile := profile
	profile = ""
	defer func() { profile = resetProfile }()

	blocked := strings.Replace(journeyResponseFixture,
		`"allowed":true`, `"allowed":false,"reason":"acceptance gate pending"`, 1)

	cases := []struct {
		name     string
		token    string
		status   int
		body     string
		wantCode int
	}{
		{"blocked", "jwt", http.StatusOK, blocked, ExitBlocked},
		{"unauthorized", "jwt", http.StatusUnauthorized, `{"error":"unauthenticated"}`, ExitNotAuthenticated},
		{"contract error", "jwt", http.StatusOK, `{"data":`, ExitError},
		{"no token", "", http.StatusOK, journeyResponseFixture, ExitNotAuthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.SetPathForTests(t.TempDir())
			if err := config.SaveProfile("", &config.Config{Token: tc.token, APIURL: "stub"}); err != nil {
				t.Fatalf("save config: %v", err)
			}
			srv := stubJourneyServer(t, tc.status, tc.body)
			defer srv.Close()

			root := rootCmd
			root.SetArgs([]string{"passo", "status", "-p", "42", "--json", "--api-url", srv.URL})
			root.SetOut(nil)
			root.SetErr(nil)
			t.Cleanup(func() { root.SetArgs(nil) })

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected exit-error for case %s", tc.name)
			}
			var ec ExitCoder
			if !errorsAs(err, &ec) {
				t.Fatalf("error is not an ExitCoder: %v", err)
			}
			if ec.ExitCode() != tc.wantCode {
				t.Fatalf("exit code = %d, want %d", ec.ExitCode(), tc.wantCode)
			}
		})
	}
}

// errorsAs is a tiny indirection so the test file keeps a single errors import.
func errorsAs(err error, target *ExitCoder) bool {
	if ec, ok := err.(ExitCoder); ok {
		*target = ec
		return true
	}
	return false
}
