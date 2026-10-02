package passoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testOAuth(server *httptest.Server) *OAuth {
	p := Profile{Name: "staging", APIURL: "https://api.example.test", IssuerURL: server.URL, ClientID: "client", ConsoleURL: "https://console.example.test"}
	return &OAuth{Profile: p, HTTPClient: server.Client(), wait: func(context.Context, time.Duration) error { return nil }}
}

func TestDevicePendingSlowDownThenSuccess(t *testing.T) {
	attempt := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/protocol/openid-connect/token" {
			t.Errorf("path = %s", r.URL.Path)
		}
		attempt++
		switch attempt {
		case 1:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":60}`))
		}
	}))
	defer s.Close()
	o := testOAuth(s)
	var waits []time.Duration
	o.wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	tok, err := o.CompleteDevice(context.Background(), DeviceAuthorization{DeviceCode: "dev", ExpiresIn: 60, Interval: 2})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access" || tok.RefreshToken != "refresh" || len(waits) != 3 || waits[0] != 2*time.Second || waits[2] != 7*time.Second {
		t.Fatalf("token=%+v waits=%v", tok, waits)
	}
}

func TestBeginDeviceAndRefresh(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/protocol/openid-connect/auth/device" {
			_ = r.ParseForm()
			if r.Form.Get("scope") != "openid profile offline_access" {
				t.Errorf("scope=%q", r.Form.Get("scope"))
			}
			_, _ = w.Write([]byte(`{"device_code":"d","user_code":"u","verification_uri":"https://verify.test","expires_in":90}`))
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "secret" {
			t.Errorf("form=%v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"new","expires_in":10}`))
	}))
	defer s.Close()
	o := testOAuth(s)
	auth, err := o.BeginDevice(context.Background())
	if err != nil || auth.DeviceCode != "d" {
		t.Fatalf("auth=%+v err=%v", auth, err)
	}
	tok, err := o.Refresh(context.Background(), "secret")
	if err != nil || tok.AccessToken != "new" || tok.RefreshToken != "secret" {
		t.Fatalf("token=%+v err=%v", tok, err)
	}
}

func TestDevicePKCEChallengeBoundToEachStart(t *testing.T) {
	challenges := map[string]string{}
	starts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/protocol/openid-connect/auth/device":
			_ = r.ParseForm()
			if r.Form.Get("code_challenge_method") != "S256" || r.Form.Get("code_challenge") == "" {
				t.Errorf("missing device PKCE parameters")
			}
			starts++
			deviceCode := fmt.Sprintf("device-%d", starts)
			challenges[deviceCode] = r.Form.Get("code_challenge")
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": deviceCode, "user_code": "user", "verification_uri": "https://verify.test", "expires_in": 30})
		case "/protocol/openid-connect/token":
			_ = r.ParseForm()
			verifier := r.Form.Get("code_verifier")
			hash := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
			if verifier == "" || gotChallenge != challenges[r.Form.Get("device_code")] {
				t.Errorf("device PKCE verifier did not match its challenge")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "expires_in": 30})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer s.Close()
	o := testOAuth(s)
	auth1, err := o.BeginDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth2, err := o.BeginDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if auth1.verifier == "" || auth2.verifier == "" || auth1.verifier == auth2.verifier {
		t.Fatal("device starts did not receive distinct private verifiers")
	}
	for _, auth := range []DeviceAuthorization{auth1, auth2} {
		if _, err := o.CompleteDevice(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInjectedRedirectPolicyCannotRedirectTokenRequest(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		_, _ = w.Write([]byte(`{"access_token":"wrong","expires_in":60}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()
	o := testOAuth(origin)
	o.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	if _, err := o.Refresh(context.Background(), "refresh"); err == nil {
		t.Fatal("expected redirected token request to fail")
	}
	if targetCalls != 0 {
		t.Fatalf("redirect target received %d requests", targetCalls)
	}
}

func TestCompleteDeviceCancellationAndWaitBound(t *testing.T) {
	var requests int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer s.Close()
	o := testOAuth(s)
	ctx, cancel := context.WithCancel(context.Background())
	var requestedWait time.Duration
	o.wait = func(waitCtx context.Context, d time.Duration) error {
		requestedWait = d
		cancel()
		return waitCtx.Err()
	}
	_, err := o.CompleteDevice(ctx, DeviceAuthorization{DeviceCode: "dev", ExpiresIn: 10, Interval: 60})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if requestedWait <= 0 || requestedWait > 10*time.Second {
		t.Fatalf("wait = %v, want at most device expiry", requestedWait)
	}
	if requests != 0 {
		t.Fatalf("made %d token requests after cancellation", requests)
	}
}

func TestTokenErrorsDoNotLeakResponse(t *testing.T) {
	secret := "private-server-description"
	for _, body := range []string{`{"error":"invalid_grant","error_description":"` + secret + `"}`, `{}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); _, _ = w.Write([]byte(body)) }))
		o := testOAuth(s)
		_, err := o.Refresh(context.Background(), "refresh")
		s.Close()
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestPKCERejectsBadStateAndCompletes(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") == "" || r.Form.Get("code_verifier") == "" {
			t.Errorf("form=%v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"ok","expires_in":3600}`))
	}))
	defer s.Close()
	o := testOAuth(s)
	var announced string
	done := make(chan struct{})
	type loginResult struct {
		token Token
		err   error
	}
	result := make(chan loginResult, 1)
	go func() {
		token, err := o.LoginPKCE(context.Background(), func(v string) error { announced = v; close(done); return nil })
		result <- loginResult{token, err}
	}()
	<-done
	u, err := url.Parse(announced)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	callback := q.Get("redirect_uri")
	redirect, err := url.Parse(callback)
	if err != nil {
		t.Fatal(err)
	}
	bad := redirect.String() + "?state=wrong&code=bad"
	resp, err := http.Get(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("bad state status=%d", resp.StatusCode)
	}
	good := redirect.String() + "?state=" + url.QueryEscape(q.Get("state")) + "&code=code"
	resp, err = http.Get(good)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("good status=%d", resp.StatusCode)
	}
	select {
	case result := <-result:
		if result.err != nil || result.token.AccessToken != "ok" {
			t.Fatalf("token=%+v err=%v", result.token, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not finish")
	}
}

func TestPKCECancellationAndAnnounceFailureCleanup(t *testing.T) {
	for _, announceErr := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
		o := testOAuth(s)
		ctx, cancel := context.WithCancel(context.Background())
		if !announceErr {
			cancel()
		}
		_, err := o.LoginPKCE(ctx, func(string) error {
			if announceErr {
				return errors.New("secret")
			}
			return nil
		})
		cancel()
		s.Close()
		if err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestTokenExpiresAtUsesExpiresIn(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "expires_in": 30})
	}))
	defer s.Close()
	before := time.Now()
	token, err := testOAuth(s).Refresh(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if token.ExpiresAt.Before(before.Add(29 * time.Second)) {
		t.Fatalf("expiry=%v", token.ExpiresAt)
	}
}

func TestTokenResponseRequiresPositiveExpiry(t *testing.T) {
	for _, expiry := range []int{0, -1} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"a","expires_in":` + fmt.Sprint(expiry) + `}`))
		}))
		_, err := testOAuth(s).Refresh(context.Background(), "r")
		s.Close()
		if err == nil || !strings.Contains(err.Error(), "valid expiry") {
			t.Fatalf("expiry %d: error = %v", expiry, err)
		}
	}
}
