package passocli

import (
	"bytes"
	"context"
	"encoding/json"
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
		_, _ = io.WriteString(w, `{"data":{"stage":"gate","primaryAction":{"label":"Review"}},"blockers":[{"code":"unknown"}]}`)
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
		_, _ = io.WriteString(w, `{"data":{"stage":"board"}}`)
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
