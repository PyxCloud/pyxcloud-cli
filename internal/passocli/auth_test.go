package passocli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteEmitsOneJSONFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--json", "status"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), `"code":"project_required"`) {
		t.Fatalf("unexpected JSON output %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr %q", errOut.String())
	}
}

type promptWriter struct{ prompt chan string }

func (w *promptWriter) Write(p []byte) (int, error) {
	line := string(p)
	select {
	case w.prompt <- line:
	default:
	}
	return len(p), nil
}

func TestExecuteStreamsPKCEPromptBeforeTokenExchange(t *testing.T) {
	var exchanges atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/protocol/openid-connect/token" {
			http.NotFound(w, r)
			return
		}
		exchanges.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"test-access","refresh_token":"test-refresh","expires_in":3600}`)
	}))
	defer issuer.Close()
	t.Setenv("PASSO_API_URL", issuer.URL)
	t.Setenv("PASSO_ISSUER_URL", issuer.URL)
	t.Setenv("PASSO_CONSOLE_URL", issuer.URL)
	store := &memoryStore{}
	prompt := make(chan string, 4)
	writer := &promptWriter{prompt: prompt}
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- execute(ctx, []string{"--json", "login", "--ledger", t.TempDir() + "/ledger.json"}, &out, writer, Options{Store: store})
	}()
	var line string
	select {
	case line = <-prompt:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("login prompt was buffered until authorization completed")
	}
	fields := strings.Fields(line)
	authRaw := fields[len(fields)-1]
	authURL, err := url.Parse(authRaw)
	if err != nil {
		t.Fatalf("invalid authorization URL %q: %v", authRaw, err)
	}
	if exchanges.Load() != 0 {
		t.Fatal("token exchange occurred before callback")
	}
	callback := authURL.Query().Get("redirect_uri")
	state := authURL.Query().Get("state")
	response, err := http.Get(callback + "?code=test-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("execute exit=%d output=%s", code, out.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not finish after callback")
	}
	if exchanges.Load() != 1 || store.saved != 1 {
		t.Fatalf("token exchanges=%d credentials saved=%d", exchanges.Load(), store.saved)
	}
}
