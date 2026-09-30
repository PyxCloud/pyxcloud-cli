package passotransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoAddsAuthJSONAndIdempotencyHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") != "key-1" {
			t.Errorf("unexpected headers: %#v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name":"x"}` {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("X-Request-Id", "req-1")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	c := New(server.URL, func(context.Context) (string, error) { return "secret-token", nil })
	got, err := c.Do(context.Background(), http.MethodPost, "/v1/items", json.RawMessage(`{"name":"x"}`), "key-1")
	if err != nil || got.RequestID != "req-1" || string(got.Body) != `{"ok":true}` {
		t.Fatalf("got %#v, err %v", got, err)
	}
}

func TestDoPreservesBasePath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.Write([]byte(`{}`)) }))
	defer server.Close()
	c := New(server.URL+"/prefix/", nil)
	_, err := c.Do(context.Background(), http.MethodGet, "/v1/items", nil, "")
	if err != nil || path != "/prefix/v1/items" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestDoRejectsUnsafePathBeforeTokenLookup(t *testing.T) {
	calls := 0
	c := New("https://api.example/base", func(context.Context) (string, error) { calls++; return "secret", nil })
	for _, path := range []string{"https://evil.test/x", "//evil.test/x", "/x#frag", "/x\r\nHost: evil"} {
		if _, err := c.Do(context.Background(), http.MethodGet, path, nil, ""); err == nil {
			t.Errorf("accepted path %q", path)
		}
	}
	if calls != 0 {
		t.Fatalf("token called %d times", calls)
	}
}

func TestNewRedirectIsRejected(t *testing.T) {
	remoteHit := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { remoteHit = true }))
	defer remote.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, remote.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err := New(redirect.URL, func(context.Context) (string, error) { return "secret", nil }).Do(context.Background(), http.MethodGet, "/", nil, "")
	if err == nil || remoteHit {
		t.Fatalf("err=%v remoteHit=%v", err, remoteHit)
	}
}

func TestDoPreservesCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := New(server.URL, nil).Do(ctx, http.MethodGet, "/", nil, ""); done <- err }()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestDoLimitsResponseAndParsesAPIErrorCode(t *testing.T) {
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 2*1024*1024+1)) }))
	defer large.Close()
	if _, err := New(large.URL, nil).Do(context.Background(), http.MethodGet, "/", nil, ""); err == nil {
		t.Fatal("oversized response accepted")
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "rid")
		w.WriteHeader(422)
		io.WriteString(w, `{"error":"bad_input","message":"private details"}`)
	}))
	defer api.Close()
	_, err := New(api.URL, nil).Do(context.Background(), http.MethodGet, "/", nil, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "bad_input" || apiErr.RequestID != "rid" || strings.Contains(err.Error(), "private details") {
		t.Fatalf("err=%#v", err)
	}
}

func TestInvalidJSONNeverSentAndErrorsAreSanitized(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer server.Close()
	_, err := New(server.URL, func(context.Context) (string, error) { return "secret-token", errors.New("secret-token leaked") }).Do(context.Background(), http.MethodPost, "/", json.RawMessage("{"), "")
	if err == nil || hit || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err=%v hit=%v", err, hit)
	}
	_, err = New(server.URL, func(context.Context) (string, error) { return "", errors.New("password=private") }).Do(context.Background(), http.MethodGet, "/", nil, "")
	if err == nil || err.Error() != "authentication unavailable" || strings.Contains(err.Error(), "private") {
		t.Fatalf("authentication error was not sanitized: %v", err)
	}
}
