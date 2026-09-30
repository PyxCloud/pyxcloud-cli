package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// TestDeviceLoginStubbedIdP proves the RFC 8628 device flow end-to-end against
// a stubbed IdP (httptest, no network): device authorization, polling through
// authorization_pending, token grant, and profile-scoped config persistence.
func TestDeviceLoginStubbedIdP(t *testing.T) {
	config.SetPathForTests(t.TempDir())
	devicePollFallback = 0
	defer func() { devicePollFallback = 5 }()

	var pollCount int
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/protocol/openid-connect/auth/device":
			if r.Method != http.MethodPost {
				t.Errorf("device endpoint: expected POST, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := r.FormValue("client_id"); got != "pyxcloud-cli" {
				t.Errorf("client_id = %q, want pyxcloud-cli", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_code":"dev-123","user_code":"ABCD-EFGH","verification_uri":"http://example.com/verify","verification_uri_complete":"http://example.com/verify?code=ABCD-EFGH","expires_in":600,"interval":0}`))
		case "/protocol/openid-connect/token":
			pollCount++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := r.FormValue("grant_type"); got != deviceGrantType {
				t.Errorf("grant_type = %q, want %q", got, deviceGrantType)
			}
			if got := r.FormValue("device_code"); got != "dev-123" {
				t.Errorf("device_code = %q, want dev-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			if pollCount == 1 {
				// First poll: the user has not approved yet (RFC 8628 §3.5).
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at-xyz","refresh_token":"rt-xyz","token_type":"Bearer"}`))
		default:
			t.Errorf("unexpected IdP request to %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()

	t.Setenv("HOME", t.TempDir()) // keep profile config out of the real home
	t.Setenv("HOME", t.TempDir()) // keep profile config out of the real home
	resetProfile := profile
	profile = "sandbox"
	defer func() { profile = resetProfile }()

	// Interval 0 is normalized to 1s inside deviceLogin; poll backoff must be
	// short in tests, so drive the flow with a stubbed sleeper is unnecessary:
	// the first poll pends, the second approves, total wait ≈ 2s.
	if err := deviceLogin(idp.URL, "pyxcloud-cli", "https://api.example.test"); err != nil {
		t.Fatalf("deviceLogin: %v", err)
	}
	if pollCount < 2 {
		t.Fatalf("expected at least 2 token polls (pending + ok), got %d", pollCount)
	}

	cfg, err := config.LoadProfile("sandbox")
	if err != nil {
		t.Fatalf("load sandbox profile config: %v", err)
	}
	if cfg.Token != "at-xyz" || cfg.RefreshToken != "rt-xyz" {
		t.Errorf("stored tokens = %q/%q, want at-xyz/rt-xyz", cfg.Token, cfg.RefreshToken)
	}
	if cfg.APIURL != "https://api.example.test" {
		t.Errorf("APIURL = %q", cfg.APIURL)
	}
}

// TestDeviceLoginDenied proves a denied device grant surfaces the IdP error
// instead of polling forever.
func TestDeviceLoginDenied(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/protocol/openid-connect/auth/device" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_code":"dev-9","user_code":"ZZ","verification_uri":"http://example.com/verify","expires_in":600,"interval":0}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer idp.Close()

	t.Setenv("HOME", t.TempDir())
	resetProfile := profile
	profile = ""
	defer func() { profile = resetProfile }()

	err := deviceLogin(idp.URL, "pyxcloud-cli", "https://api.example.test")
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("expected access_denied error, got %v", err)
	}
}
