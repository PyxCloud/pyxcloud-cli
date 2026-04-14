package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
	"github.com/spf13/cobra"
)

const (
	callbackPath      = "/callback"
	contentTypeHeader = "Content-Type"
	contentTypeHTML   = "text/html"
	errorMsgToken     = "{{ERROR_MSG}}"
	htmlSuccess       = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>PyxCloud CLI — Authenticated</title>
<link href="https://fonts.googleapis.com/css2?family=Poppins:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Poppins',system-ui,sans-serif;height:100vh;width:100vw;overflow:hidden;background:#fff}
.container{display:flex;height:100vh;width:100%}
.left{flex:1;display:flex;flex-direction:column;justify-content:center;align-items:center;padding:48px 60px;max-width:50%}
.right{flex:1;background:#f4f6f8;display:flex;align-items:center;justify-content:center;position:relative;overflow:hidden}
.right::before{content:'';position:absolute;width:600px;height:600px;border-radius:50%;background:radial-gradient(circle,rgba(0,118,209,0.08) 0%,transparent 70%);top:50%;left:50%;transform:translate(-50%,-50%)}
.logo{display:flex;align-items:center;gap:10px;margin-bottom:48px}
.logo svg{height:36px;width:36px}
.logo-text{font-size:22px;font-weight:700;line-height:1}
.logo-pyx{color:#282828}
.logo-cloud{color:#0076d1}
.success-icon{width:64px;height:64px;background:#e8f5e9;border-radius:50%;display:flex;align-items:center;justify-content:center;margin-bottom:24px}
.success-icon svg{width:32px;height:32px;stroke:#2e7d32;fill:none;stroke-width:2.5;stroke-linecap:round;stroke-linejoin:round}
h1{font-size:24px;font-weight:600;color:#1a1a1a;margin-bottom:8px}
p{font-size:14px;color:#666;line-height:1.6;max-width:360px}
.hint{margin-top:24px;padding:12px 16px;background:#f8f9fa;border-radius:8px;font-size:13px;color:#888;display:flex;align-items:center;gap:8px}
.hint code{background:#e8ecef;padding:2px 8px;border-radius:4px;font-family:'SF Mono',Monaco,monospace;font-size:12px;color:#333}
.right-content{position:relative;z-index:1;text-align:center;padding:40px}
.right-content svg{width:280px;height:280px;opacity:0.9}
.terminal-icon{display:inline-flex;align-items:center;gap:6px;margin-top:32px;padding:8px 16px;background:rgba(0,118,209,0.06);border-radius:8px;font-size:13px;color:#0076d1;font-weight:500}
@media(max-width:900px){.right{display:none}.left{max-width:100%}}
</style>
</head>
<body>
<div class="container">
  <div class="left">
    <div style="width:100%;max-width:400px">
      <div class="logo">
        <svg viewBox="0 0 40 40" fill="none"><rect width="40" height="40" rx="8" fill="#0076d1"/><path d="M12 20l5 5 11-11" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>
        <span class="logo-text"><span class="logo-pyx">pyx</span><span class="logo-cloud">cloud</span></span>
      </div>
      <div class="success-icon">
        <svg viewBox="0 0 24 24"><path d="M20 6L9 17l-5-5"/></svg>
      </div>
      <h1>Authenticated</h1>
      <p>You're all set. You can close this browser tab and return to your terminal.</p>
      <div class="hint">
        <span>Try</span> <code>pyxcloud compare &lt;id&gt; &lt;version&gt;</code>
      </div>
    </div>
  </div>
  <div class="right">
    <div class="right-content">
      <svg viewBox="0 0 280 280" fill="none">
        <rect x="30" y="60" width="220" height="160" rx="12" fill="#fff" stroke="#e0e0e0" stroke-width="1.5"/>
        <rect x="30" y="60" width="220" height="32" rx="12" fill="#f8f9fa"/>
        <circle cx="52" cy="76" r="5" fill="#ff5f57"/><circle cx="68" cy="76" r="5" fill="#febc2e"/><circle cx="84" cy="76" r="5" fill="#28c840"/>
        <rect x="50" y="108" width="180" height="8" rx="4" fill="#e8ecef"/>
        <rect x="50" y="124" width="140" height="8" rx="4" fill="#e8ecef"/>
        <rect x="50" y="148" width="80" height="24" rx="6" fill="#0076d1"/>
        <text x="90" y="164" font-family="Poppins,sans-serif" font-size="10" fill="#fff" text-anchor="middle">Connected</text>
        <rect x="50" y="184" width="100" height="6" rx="3" fill="#e8f5e9"/>
        <rect x="50" y="196" width="60" height="6" rx="3" fill="#e8f5e9"/>
        <path d="M140 30l8 14h-16z" fill="#0076d1" opacity="0.15"/>
        <circle cx="240" cy="50" r="16" fill="#0076d1" opacity="0.08"/>
        <circle cx="40" cy="250" r="12" fill="#0076d1" opacity="0.06"/>
      </svg>
      <div class="terminal-icon">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>
        CLI session active
      </div>
    </div>
  </div>
</div>
</body>
</html>`
	htmlError = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>PyxCloud CLI — Error</title>
<link href="https://fonts.googleapis.com/css2?family=Poppins:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Poppins',system-ui,sans-serif;height:100vh;width:100vw;overflow:hidden;background:#fff}
.container{display:flex;height:100vh;width:100%}
.left{flex:1;display:flex;flex-direction:column;justify-content:center;align-items:center;padding:48px 60px;max-width:50%}
.right{flex:1;background:#f4f6f8;display:flex;align-items:center;justify-content:center;position:relative;overflow:hidden}
.right::before{content:'';position:absolute;width:600px;height:600px;border-radius:50%;background:radial-gradient(circle,rgba(211,47,47,0.06) 0%,transparent 70%);top:50%;left:50%;transform:translate(-50%,-50%)}
.logo{display:flex;align-items:center;gap:10px;margin-bottom:48px}
.logo svg{height:36px;width:36px}
.logo-text{font-size:22px;font-weight:700;line-height:1}
.logo-pyx{color:#282828}
.logo-cloud{color:#0076d1}
.error-icon{width:64px;height:64px;background:#fbeded;border-radius:50%;display:flex;align-items:center;justify-content:center;margin-bottom:24px}
.error-icon svg{width:32px;height:32px;stroke:#d32f2f;fill:none;stroke-width:2.5;stroke-linecap:round;stroke-linejoin:round}
h1{font-size:24px;font-weight:600;color:#1a1a1a;margin-bottom:8px}
.error-msg{font-size:14px;color:#d32f2f;background:#fbeded;padding:12px 16px;border-radius:8px;border:1px solid #f5c6cb;margin-top:16px;max-width:360px;word-break:break-word}
p{font-size:14px;color:#666;line-height:1.6;max-width:360px}
@media(max-width:900px){.right{display:none}.left{max-width:100%}}
</style>
</head>
<body>
<div class="container">
  <div class="left">
    <div style="width:100%;max-width:400px">
      <div class="logo">
        <svg viewBox="0 0 40 40" fill="none"><rect width="40" height="40" rx="8" fill="#d32f2f"/><path d="M14 14l12 12M26 14L14 26" stroke="#fff" stroke-width="3" stroke-linecap="round"/></svg>
        <span class="logo-text"><span class="logo-pyx">pyx</span><span class="logo-cloud">cloud</span></span>
      </div>
      <div class="error-icon">
        <svg viewBox="0 0 24 24"><path d="M18 6L6 18M6 6l12 12"/></svg>
      </div>
      <h1>Authentication failed</h1>
      <p>Something went wrong during the login process. Please try again from your terminal.</p>
      <div class="error-msg">{{ERROR_MSG}}</div>
    </div>
  </div>
  <div class="right"></div>
</div>
</body>
</html>`
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with PyxCloud",
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login via browser (SSO)",
	Long: `Opens your browser for SSO authentication with PyxCloud.
A local server is started to receive the OAuth2 callback.

For CI/CD (non-interactive), use --token with a Keycloak JWT or offline token:
  pyxcloud auth login --token <jwt_or_refresh_token>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		token, _ := cmd.Flags().GetString("token")
		backendURL, _ := cmd.Flags().GetString("url")
		if backendURL == "" {
			backendURL = "https://beta-api.pyxcloud.io"
		}

		if token != "" {
			return loginWithToken(token, backendURL)
		}
		return loginWithBrowser(cmd, backendURL)
	},
}

func loginWithToken(token, backendURL string) error {
	// Detect token type: pyxc_* = opaque PAT, otherwise = JWT access token
	isPAT := strings.HasPrefix(token, "pyxc_")

	cfg := &config.Config{APIURL: backendURL}
	if isPAT {
		// PAT mode: store as refresh token — backend's /cli/refresh validates and returns JWT
		cfg.RefreshToken = token
		cfg.Token = "" // populated on first API call via ensureToken()
	} else {
		// Direct JWT access token mode
		cfg.Token = token
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("Authenticated with token.")
	fmt.Printf("  API: %s\n", backendURL)
	l := len(token)
	if l > 20 {
		l = 20
	}
	fmt.Printf("  Token: %s...\n", token[:l])
	if isPAT {
		fmt.Println("  Type: API key (exchanges for short-lived JWT before each call)")
	}
	return nil
}

func loginWithBrowser(cmd *cobra.Command, backendURL string) error {
	authURL, _ := cmd.Flags().GetString("auth-url")
	clientID, _ := cmd.Flags().GetString("client-id")
	if authURL == "" {
		authURL = "https://beta-auth.pyxcloud.io/realms/pyx"
	}
	if clientID == "" {
		clientID = "pyxcloud-cli"
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot start local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d%s", port, callbackPath)

	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)
	state := generateState()

	// Request offline_access scope for long-lived refresh token
	authorizeURL := fmt.Sprintf(
		"%s/protocol/openid-connect/auth?client_id=%s&response_type=code&redirect_uri=%s&scope=openid+profile+email+offline_access&state=%s&code_challenge=%s&code_challenge_method=S256",
		authURL,
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
	)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(htmlError, errorMsgToken, "Invalid state parameter", 1))
			errCh <- fmt.Errorf("state mismatch")
			return
		}
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(htmlError, errorMsgToken, desc, 1))
			errCh <- fmt.Errorf("auth error: %s — %s", errParam, desc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(htmlError, errorMsgToken, "No authorization code received", 1))
			errCh <- fmt.Errorf("no code in callback")
			return
		}
		w.Header().Set(contentTypeHeader, contentTypeHTML)
		io.WriteString(w, htmlSuccess)
		codeCh <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		if serveErr := server.Serve(listener); serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	fmt.Println("Opening browser for authentication...")
	logv("If the browser does not open, visit:\n   %s", authorizeURL)
	openBrowser(authorizeURL)

	var authCode string
	select {
	case authCode = <-codeCh:
	case authErr := <-errCh:
		_ = server.Shutdown(context.Background())
		return authErr
	case <-time.After(120 * time.Second):
		_ = server.Shutdown(context.Background())
		return fmt.Errorf("authentication timed out (120s)")
	}
	_ = server.Shutdown(context.Background())

	logv("Exchanging authorization code...")
	tokenEndpoint := authURL + "/protocol/openid-connect/token"
	accessToken, refreshToken, err := exchangeCodeForTokens(tokenEndpoint, clientID, authCode, redirectURI, verifier)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Store tokens — the refresh_token with offline_access scope persists across restarts
	cfg := &config.Config{
		Token:        accessToken,
		RefreshToken: refreshToken,
		APIURL:       backendURL,
		AuthURL:      authURL,
		ClientID:     clientID,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("Authenticated via SSO.")
	fmt.Printf("  API: %s\n", backendURL)
	if refreshToken != "" {
		fmt.Println("  Offline token stored — sessions persist across restarts")
	}
	return nil
}

// exchangeCodeForTokens exchanges an auth code for access_token + refresh_token.
func exchangeCodeForTokens(tokenEndpoint, clientID, code, redirectURI, verifier string) (string, string, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", clientID)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("code_verifier", verifier)

	resp, err := http.PostForm(tokenEndpoint, data)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("token endpoint returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode token response: %w", err)
	}

	accessToken, ok := result["access_token"].(string)
	if !ok || accessToken == "" {
		return "", "", fmt.Errorf("no access_token in response")
	}

	refreshToken, _ := result["refresh_token"].(string)
	return accessToken, refreshToken, nil
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.Save(&config.Config{}); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}

// ── PKCE helpers ──

func generateCodeVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func generateState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "linux":
		cmd = exec.Command("xdg-open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func init() {
	loginCmd.Flags().String("token", "", "JWT or offline token for CI/CD (non-interactive, skips browser)")
	loginCmd.Flags().String("url", "https://beta-api.pyxcloud.io", "PyxCloud API URL")
	loginCmd.Flags().String("auth-url", "https://beta-auth.pyxcloud.io/realms/pyx", "Keycloak realm URL")
	loginCmd.Flags().String("client-id", "pyxcloud-cli", "OAuth2 client ID")

	authCmd.AddCommand(loginCmd, logoutCmd)
	rootCmd.AddCommand(authCmd)
}
