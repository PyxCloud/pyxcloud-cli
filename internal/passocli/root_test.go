package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
)

type memoryStore struct {
	token          passoauth.Token
	saved, deleted int
}

func (s *memoryStore) Load(string) (passoauth.Token, error)   { return s.token, nil }
func (s *memoryStore) Save(_ string, t passoauth.Token) error { s.token = t; s.saved++; return nil }
func (s *memoryStore) Delete(string) error                    { s.deleted++; return nil }

func TestIndependentCommandsAndProfileProjectMismatch(t *testing.T) {
	store := &memoryStore{}
	var out1, err1, out2, err2 bytes.Buffer
	one := New(Options{Out: &out1, Err: &err1, Store: store})
	two := New(Options{Out: &out2, Err: &err2, Store: store})
	one.SetArgs([]string{"--profile", "sandbox", "--project", "11", "status"})
	two.SetArgs([]string{"--profile", "sandbox", "--project", "12", "status"})
	if err := one.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected missing credentials")
	}
	if err := two.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected missing credentials")
	}
	if !strings.Contains(out1.String(), "") || out1.Len() != 0 || out2.Len() != 0 {
		t.Fatal("failed commands wrote success output")
	}
}

func TestStatusReturnsJourneyEnvelopeWithoutMutation(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/vibe/projects/42/journey" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":{"projectId":42,"stage":"gate","primaryAction":{"key":"open_board","label":"Review"}},"blockers":[{"code":"unknown"}]}`)
	}))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	store := &memoryStore{token: passoauth.Token{AccessToken: "ephemeral", ExpiresAt: time.Now().Add(time.Hour)}}
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "42", "--json", "status"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	var got Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid result: %s: %v", out.String(), err)
	}
	if got.Stage != "gate" || got.Status != "observed" || got.NextAction == nil || !strings.Contains(string(got.Data), "unknown") {
		t.Fatalf("unexpected result %#v", got)
	}
}

func TestRuntimeRejectsLedgerScopeMismatchBeforeRequest(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ledger.json"
	if err := osWriteFile(path, []byte(`{"schemaVersion":1,"profile":"staging","projectId":9,"operations":{}}`)); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: &memoryStore{}})
	cmd.SetArgs([]string{"--profile", "sandbox", "--project", "42", "--ledger", path, "status"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "scope_mismatch") {
		t.Fatalf("got %v", err)
	}
}

func osWriteFile(p string, b []byte) error { return os.WriteFile(p, b, 0600) }

func TestStatusRefreshesExpiringTokenAndCancellation(t *testing.T) {
	var refreshed int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			refreshed++
			_, _ = io.WriteString(w, `{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh-access" {
			t.Errorf("authorization not refreshed: %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"data":{"projectId":7,"stage":"board","primaryAction":{"key":"open_board"}}}`)
	}))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	store := &memoryStore{token: passoauth.Token{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Second)}}
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "7", "--json", "status"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshed != 1 || store.saved != 1 || store.token.AccessToken != "fresh-access" {
		t.Fatalf("refresh=%d saved=%d token=%q", refreshed, store.saved, store.token.AccessToken)
	}
}

func TestStatusHonorsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	store := &memoryStore{token: passoauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "7", "status"})
	if err := cmd.ExecuteContext(ctx); err == nil || ExitCode(err) != 20 {
		t.Fatalf("expected canceled command error, got %v", err)
	}
}

func TestStatusRejectsMalformedOrMismatchedJourney(t *testing.T) {
	cases := []struct{ name, body string }{
		{"missing data", `{"data":null}`},
		{"missing project", `{"data":{"stage":"gate","primaryAction":{"key":"open_board"}}}`},
		{"wrong project", `{"data":{"projectId":8,"stage":"gate","primaryAction":{"key":"open_board"}}}`},
		{"unknown stage", `{"data":{"projectId":7,"stage":"unknown","primaryAction":{"key":"open_board"}}}`},
		{"missing action key", `{"data":{"projectId":7,"stage":"gate","primaryAction":{"label":"Review"}}}`},
		{"unknown action key", `{"data":{"projectId":7,"stage":"gate","primaryAction":{"key":"invented_action"}}}`},
		{"action not object", `{"data":{"projectId":7,"stage":"gate","primaryAction":[]}}`},
		{"malformed json", `not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer srv.Close()
			t.Setenv("PASSO_API_URL", srv.URL)
			t.Setenv("PASSO_ISSUER_URL", srv.URL)
			t.Setenv("PASSO_CONSOLE_URL", srv.URL)
			var out, errOut bytes.Buffer
			cmd := New(Options{Out: &out, Err: &errOut, Store: &memoryStore{token: passoauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}}})
			cmd.SetArgs([]string{"--project", "7", "--json", "status"})
			err := cmd.ExecuteContext(context.Background())
			var ee *ExitError
			if !errors.As(err, &ee) || ee.ExitCode != 30 || ee.Code != "invalid_journey_response" {
				t.Fatalf("got %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("reported success for invalid journey: %s", out.String())
			}
		})
	}
}

func TestHumanStatusShowsSanitizedNextActionAndBrowserURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"projectId":7,"stage":"gate","primaryAction":{"key":"freeze","label":"Seal release\nInjected","href":"https://console.example/seal\u001b[31m"}}}`)
	}))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	store := &memoryStore{token: passoauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}}
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "7", "status"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Next action: freeze", "Label: Seal release Injected", "URL: https://console.example/seal[31m"} {
		if !strings.Contains(got, want) {
			t.Fatalf("human status omitted %q: %q", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\r") || strings.Count(got, "\n") > 8 {
		t.Fatalf("human status contains controls or exceeds 8 lines: %q", got)
	}
}

func TestRuntimeRejectsConflictingLedgerIdentities(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{{"--version", "v-other"}, {"--release", "r-other"}, {"--run", "run-other"}} {
		t.Run(tc.flag, func(t *testing.T) {
			path := t.TempDir() + "/ledger.json"
			if err := osWriteFile(path, []byte(`{"schemaVersion":1,"profile":"sandbox","projectId":7,"versionId":"v-ledger","releaseId":"r-ledger","runId":"run-ledger","operations":{}}`)); err != nil {
				t.Fatal(err)
			}
			args := []string{"--project", "7", "--ledger", path, tc.flag, tc.value, "status"}
			cmd := New(Options{Out: io.Discard, Err: io.Discard, Store: &memoryStore{}})
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(context.Background())
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != "scope_mismatch" {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestMonitorDefaultsToOneShotAndWatchReturnsTypedDeadline(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"data":{"projectId":7,"stage":"board","primaryAction":{"key":"open_board"}}}`)
	}))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	store := &memoryStore{token: passoauth.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}}
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "7", "--json", "monitor"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("calls=%d output=%q", calls, out.String())
	}
	calls = 0
	out.Reset()
	cmd = New(Options{Out: &out, Err: &errOut, Store: store})
	cmd.SetArgs([]string{"--project", "7", "--json", "--timeout=20ms", "--poll-interval=1ms", "monitor", "--watch"})
	err := cmd.ExecuteContext(context.Background())
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != "deadline_exceeded" || ee.ExitCode != 20 {
		t.Fatalf("got %v", err)
	}
	if calls < 1 || strings.Count(out.String(), "\n") < 1 {
		t.Fatalf("calls=%d records=%q", calls, out.String())
	}
}

func TestExecuteParsesJSONFlagAndSanitizesGenericErrors(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		json bool
	}{{"--json", true}, {"--json=true", true}, {"--json=false", false}} {
		t.Run(tc.arg, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Execute(context.Background(), []string{tc.arg, "unknown-command"}, &out, &errOut)
			if code == 0 {
				t.Fatal("expected error")
			}
			if tc.json {
				var got Result
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("not JSON: %q (%v)", out.String(), err)
				}
				if got.Code != "command_failed" {
					t.Fatalf("unstable error code %q", got.Code)
				}
				if errOut.Len() != 0 {
					t.Fatalf("unexpected stderr %q", errOut.String())
				}
			} else if out.Len() != 0 || errOut.Len() == 0 {
				t.Fatalf("unexpected outputs stdout=%q stderr=%q", out.String(), errOut.String())
			}
		})
	}
}

func TestMonitorCancellationReturnsTypedError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: &memoryStore{}})
	cmd.SetArgs([]string{"--project", "1", "--json", "monitor", "--watch"})
	err := cmd.ExecuteContext(ctx)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != "canceled" || ee.ExitCode != 20 {
		t.Fatalf("got %v", err)
	}
}

func TestExecutePreservesWatchRecordsBeforeFinalTimeoutError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"data":{"projectId":7,"stage":"board","primaryAction":{"key":"open_board"}}}`)
	}))
	defer srv.Close()
	t.Setenv("PASSO_API_URL", srv.URL)
	t.Setenv("PASSO_ISSUER_URL", srv.URL)
	t.Setenv("PASSO_CONSOLE_URL", srv.URL)
	t.Setenv("PASSO_ACCESS_TOKEN", "watch-token")
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--project", "7", "--json", "--timeout=30ms", "--poll-interval=1ms", "monitor", "--watch"}, &out, &errOut)
	if code != 20 {
		t.Fatalf("exit=%d output=%q", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if calls < 1 || len(lines) < 2 {
		t.Fatalf("calls=%d records=%q", calls, out.String())
	}
	for i, line := range lines {
		var record Result
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not JSON: %q (%v)", i, line, err)
		}
		if i < len(lines)-1 && record.Status != "observed" {
			t.Fatalf("record %d is not observed: %#v", i, record)
		}
		if i == len(lines)-1 && (record.Code != "deadline_exceeded" || record.Status != "error") {
			t.Fatalf("missing final timeout record: %#v", record)
		}
	}
}
