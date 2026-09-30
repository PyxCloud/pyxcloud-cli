package passoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxOAuthResponse = 64 << 10

// OAuth implements the device-code and authorization-code OAuth flows.
type OAuth struct {
	Profile    Profile
	HTTPClient *http.Client
	wait       func(context.Context, time.Duration) error
}

// DeviceAuthorization is the information needed to authorize a device.
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

func (o *OAuth) client() *http.Client {
	if o.HTTPClient == nil {
		return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	client := *o.HTTPClient
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &client
}

func (o *OAuth) endpoint(name string) (string, error) {
	if err := ValidateProfile(o.Profile); err != nil {
		return "", err
	}
	u, err := url.Parse(o.Profile.IssuerURL)
	if err != nil {
		return "", errors.New("invalid issuer URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/protocol/openid-connect/" + name
	return u.String(), nil
}

func (o *OAuth) BeginDevice(ctx context.Context) (DeviceAuthorization, error) {
	endpoint, err := o.endpoint("auth/device")
	if err != nil {
		return DeviceAuthorization{}, err
	}
	values := url.Values{"client_id": {o.Profile.ClientID}, "scope": {"openid profile offline_access"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return DeviceAuthorization{}, errors.New("could not create device authorization request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client().Do(req)
	if err != nil {
		return DeviceAuthorization{}, errors.New("device authorization request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DeviceAuthorization{}, errors.New("device authorization request failed")
	}
	var raw struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := decodeLimited(resp.Body, &raw); err != nil || raw.DeviceCode == "" || raw.UserCode == "" || raw.VerificationURI == "" || raw.ExpiresIn <= 0 {
		return DeviceAuthorization{}, errors.New("invalid device authorization response")
	}
	return DeviceAuthorization{raw.DeviceCode, raw.UserCode, raw.VerificationURI, raw.VerificationURIComplete, raw.ExpiresIn, raw.Interval}, nil
}

func (o *OAuth) CompleteDevice(ctx context.Context, auth DeviceAuthorization) (Token, error) {
	if auth.DeviceCode == "" || auth.ExpiresIn <= 0 {
		return Token{}, errors.New("invalid device authorization")
	}
	endpoint, err := o.endpoint("token")
	if err != nil {
		return Token{}, err
	}
	interval := auth.Interval
	if interval <= 0 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	for {
		if time.Until(deadline) <= 0 {
			return Token{}, errors.New("device authorization expired")
		}
		if err := o.waitFor(ctx, time.Duration(interval)*time.Second); err != nil {
			return Token{}, err
		}
		if time.Until(deadline) <= 0 {
			return Token{}, errors.New("device authorization expired")
		}
		values := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {o.Profile.ClientID}, "device_code": {auth.DeviceCode}}
		token, status, code, err := o.tokenRequest(ctx, endpoint, values)
		if err == nil {
			return token, nil
		}
		if status != http.StatusBadRequest && status != http.StatusUnauthorized {
			return Token{}, err
		}
		switch code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return Token{}, errors.New("device authorization expired")
		default:
			return Token{}, err
		}
	}
}

func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	if refreshToken == "" {
		return Token{}, errors.New("refresh token is required")
	}
	endpoint, err := o.endpoint("token")
	if err != nil {
		return Token{}, err
	}
	values := url.Values{"grant_type": {"refresh_token"}, "client_id": {o.Profile.ClientID}, "refresh_token": {refreshToken}}
	token, _, _, err := o.tokenRequest(ctx, endpoint, values)
	return token, err
}

func (o *OAuth) LoginPKCE(ctx context.Context, announce func(string) error) (Token, error) {
	if announce == nil {
		return Token{}, errors.New("authorization URL callback is required")
	}
	endpoint, err := o.endpoint("auth")
	if err != nil {
		return Token{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, errors.New("could not start OAuth callback listener")
	}
	defer listener.Close()
	state, err := randomURL(32)
	if err != nil {
		return Token{}, errors.New("could not initialize OAuth flow")
	}
	verifier, err := randomURL(32)
	if err != nil {
		return Token{}, errors.New("could not initialize OAuth flow")
	}
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	authURL, err := url.Parse(endpoint)
	if err != nil {
		return Token{}, errors.New("invalid authorization endpoint")
	}
	q := authURL.Query()
	q.Set("client_id", o.Profile.ClientID)
	q.Set("response_type", "code")
	q.Set("scope", "openid profile offline_access")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()
	type result struct {
		code string
		err  error
	}
	resultCh := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			http.Error(w, "authorization failed", http.StatusBadRequest)
			select {
			case resultCh <- result{err: errors.New("authorization was denied")}:
			default:
			}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "Authorization complete. You can close this window.")
		select {
		case resultCh <- result{code: code}:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-serveDone }()
	if err := announce(authURL.String()); err != nil {
		return Token{}, errors.New("could not announce authorization URL")
	}
	var callback result
	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case callback = <-resultCh:
	}
	if callback.err != nil {
		return Token{}, callback.err
	}
	tokenEndpoint, err := o.endpoint("token")
	if err != nil {
		return Token{}, err
	}
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {o.Profile.ClientID}, "code": {callback.code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	token, _, _, err := o.tokenRequest(ctx, tokenEndpoint, values)
	return token, err
}

func (o *OAuth) tokenRequest(ctx context.Context, endpoint string, values url.Values) (Token, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return Token{}, 0, "", errors.New("could not create token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client().Do(req)
	if err != nil {
		return Token{}, 0, "", errors.New("token request failed")
	}
	defer resp.Body.Close()
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := decodeLimited(resp.Body, &raw); err != nil {
		return Token{}, resp.StatusCode, "", errors.New("invalid token response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Token{}, resp.StatusCode, raw.Error, errors.New("token request failed")
	}
	if raw.AccessToken == "" {
		return Token{}, resp.StatusCode, "", errors.New("token response did not contain an access token")
	}
	return Token{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)}, resp.StatusCode, "", nil
}

func decodeLimited(r io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(r, maxOAuthResponse+1))
	if err != nil {
		return err
	}
	if len(data) > maxOAuthResponse {
		return fmt.Errorf("response too large")
	}
	return json.Unmarshal(data, target)
}

func (o *OAuth) waitFor(ctx context.Context, duration time.Duration) error {
	if o.wait != nil {
		return o.wait(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomURL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
