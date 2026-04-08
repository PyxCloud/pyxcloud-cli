package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
	"github.com/pyxcloud/pyxcloud-cli/internal/shamir"
	"github.com/spf13/cobra"
)

const (
	contentTypeHeader = "Content-Type"
	contentTypeHTML   = "text/html"
	errorTarget       = "{{ERROR}}"
)

// keystoreRecoverCmd performs key recovery via Keycloak re-authentication:
//
//  1. Fetch Half-A (system share) — standard auth
//  2. Force Keycloak re-login (prompt=login) to prove user presence
//  3. Exchange fresh JWT for a step-up token via POST /auth/step-up
//  4. Fetch Half-B (recovery share) with step-up token
//  5. Combine shares and save PEM
var keystoreRecoverCmd = &cobra.Command{
	Use:   "recover",
	Short: "Download a private key (requires re-authentication)",
	Long: `Recovers a private key by combining Shamir shares.
This requires re-authentication via your browser to prove your identity.

The CLI will:
  1. Open your browser for re-authentication (Keycloak SSO)
  2. Retrieve key shares from PyxCloud and Vault
  3. Reconstruct and save the private key locally

Example:
  pyxcloud keystore recover --id 5
  pyxcloud keystore recover --id 5 --output my-key.pem`,
	RunE: func(cmd *cobra.Command, args []string) error {
		keyID, _ := cmd.Flags().GetString("id")
		output, _ := cmd.Flags().GetString("output")
		if keyID == "" {
			return fmt.Errorf("--id is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		// Step 1: Fetch Half-A (system share)
		logv("Fetching system share...")
		halfAData, err := client.KeystoreHalfA(keyID)
		if err != nil {
			return fmt.Errorf("fetch system share: %w", err)
		}
		halfA, ok := halfAData["halfA"].(string)
		if !ok || halfA == "" {
			return fmt.Errorf("system share is missing from the database")
		}
		logv("System share retrieved.")

		// Step 2: Force re-auth via Keycloak
		fmt.Println("Opening browser for re-authentication...")
		stepUpToken, err := stepUpViaKeycloak(client)
		if err != nil {
			return fmt.Errorf("step-up authentication: %w", err)
		}
		logv("Identity verified.")

		// Step 3: Fetch Half-B with step-up token
		logv("Fetching recovery share from Vault...")
		halfBData, err := client.KeystoreHalfB(keyID, stepUpToken)
		if err != nil {
			return fmt.Errorf("fetch recovery share: %w", err)
		}
		halfB, ok := halfBData["halfB"].(string)
		if !ok || halfB == "" {
			return fmt.Errorf("recovery share is missing from Vault")
		}
		if iv, ok := halfBData["iv"].(string); ok && iv != "" {
			return fmt.Errorf("this key uses legacy E2E encryption — please delete and regenerate")
		}
		logv("Recovery share retrieved.")

		// Step 4: Combine Shamir shares
		logv("Reconstructing private key...")
		privateKeyHex, err := shamir.Combine(halfA, halfB)
		if err != nil {
			return fmt.Errorf("shamir combine: %w", err)
		}
		privateKeyPEM := shamir.Hex2Str(privateKeyHex)
		if !strings.Contains(privateKeyPEM, "PRIVATE KEY") {
			return fmt.Errorf("reconstruction failed — key does not contain a valid PEM block")
		}

		// Step 5: Save
		if output == "" {
			output = fmt.Sprintf("key_%s.pem", keyID)
		}
		if err := os.WriteFile(output, []byte(privateKeyPEM), 0600); err != nil {
			return fmt.Errorf("save key: %w", err)
		}

		fmt.Printf("Private key saved to %s\n", output)
		fmt.Println("Warning: this file contains your unencrypted private key.")
		return nil
	},
}

// stepUpViaKeycloak performs a forced re-authentication via Keycloak SSO,
// then exchanges the resulting fresh JWT for a scoped step-up token.
// Same PKCE flow as `auth login`, but with prompt=login&max_age=0.
func stepUpViaKeycloak(client interface{}) (string, error) {
	// Load config for Keycloak settings
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	authURL := cfg.AuthURL
	clientID := cfg.ClientID
	if authURL == "" {
		authURL = "http://localhost:8180/realms/pyx"
	}
	if clientID == "" {
		clientID = "pyxcloud-cli"
	}

	// Local callback server
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", fmt.Errorf("start local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/step-up-callback", port)

	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)
	state := generateState()

	// Force re-auth: prompt=login&max_age=0 ensures the user must prove identity again
	authorizeURL := fmt.Sprintf(
		"%s/protocol/openid-connect/auth?client_id=%s&response_type=code&redirect_uri=%s&scope=openid&state=%s&code_challenge=%s&code_challenge_method=S256&prompt=login&max_age=0",
		authURL,
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
	)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/step-up-callback", createStepUpCallback(state, codeCh, errCh))

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	logv("Authorization URL: %s", authorizeURL)
	openBrowser(authorizeURL)

	// Wait for auth code
	var authCode string
	select {
	case authCode = <-codeCh:
	case err := <-errCh:
		return "", err
	case <-time.After(120 * time.Second):
		return "", fmt.Errorf("re-authentication timed out (2 min)")
	}

	// Exchange code for fresh access_token
	logv("Exchanging authorization code...")
	tokenEndpoint := authURL + "/protocol/openid-connect/token"
	freshToken, _, err := exchangeCodeForTokens(tokenEndpoint, clientID, authCode, redirectURI, verifier)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}

	// Exchange fresh JWT for a scoped step-up token via backend
	logv("Requesting step-up token...")
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}

	stepUpBody := map[string]string{
		"assertionId": "cli-reauth",
	}
	bodyBytes, _ := json.Marshal(stepUpBody)

	req, err := http.NewRequest("POST", strings.TrimSuffix(apiURL, "/")+"/auth/step-up", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", fmt.Errorf("create step-up request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+freshToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("step-up request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("step-up failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var stepUpResult map[string]interface{}
	if err := json.Unmarshal(respBody, &stepUpResult); err != nil {
		return "", fmt.Errorf("decode step-up response: %w", err)
	}

	stepUpToken, ok := stepUpResult["stepUpToken"].(string)
	if !ok || stepUpToken == "" {
		return "", fmt.Errorf("no step-up token in response")
	}

	return stepUpToken, nil
}

func createStepUpCallback(state string, codeCh chan string, errCh chan error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(stepUpCallbackError, errorTarget, "Invalid state — possible CSRF attack", 1))
			errCh <- fmt.Errorf("state mismatch")
			return
		}
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(stepUpCallbackError, errorTarget, desc, 1))
			errCh <- fmt.Errorf("auth error: %s — %s", errParam, desc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.Header().Set(contentTypeHeader, contentTypeHTML)
			io.WriteString(w, strings.Replace(stepUpCallbackError, errorTarget, "No authorization code received", 1))
			errCh <- fmt.Errorf("no code in callback")
			return
		}
		w.Header().Set(contentTypeHeader, contentTypeHTML)
		io.WriteString(w, stepUpCallbackSuccess)
		codeCh <- code
	}
}

// ─── Self-contained HTML for the step-up callback ───────────────────────

const stepUpCallbackSuccess = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><title>PyxCloud — Identity Verified</title>
<link href="https://fonts.googleapis.com/css2?family=Poppins:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Poppins',system-ui,sans-serif;height:100vh;display:flex;align-items:center;justify-content:center;background:#fff}
.card{text-align:center;max-width:400px;padding:48px}
.icon{width:64px;height:64px;background:#e8f5e9;border-radius:50%;display:flex;align-items:center;justify-content:center;margin:0 auto 24px}
.icon svg{width:32px;height:32px;stroke:#2e7d32;fill:none;stroke-width:2.5;stroke-linecap:round;stroke-linejoin:round}
h1{font-size:22px;font-weight:600;color:#1a1a1a;margin-bottom:8px}
p{font-size:14px;color:#666;line-height:1.6}
.hint{margin-top:20px;font-size:13px;color:#9ca3af}
</style></head>
<body><div class="card">
<div class="icon"><svg viewBox="0 0 24 24"><path d="M20 6L9 17l-5-5"/></svg></div>
<h1>Identity Verified</h1>
<p>Your key is being recovered. You can close this tab and return to the terminal.</p>
<p class="hint">Key recovery in progress.</p>
</div></body></html>`

const stepUpCallbackError = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><title>PyxCloud — Error</title>
<link href="https://fonts.googleapis.com/css2?family=Poppins:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Poppins',system-ui,sans-serif;height:100vh;display:flex;align-items:center;justify-content:center;background:#fff}
.card{text-align:center;max-width:400px;padding:48px}
.icon{width:64px;height:64px;background:#fbeded;border-radius:50%;display:flex;align-items:center;justify-content:center;margin:0 auto 24px}
.icon svg{width:32px;height:32px;stroke:#d32f2f;fill:none;stroke-width:2.5;stroke-linecap:round;stroke-linejoin:round}
h1{font-size:22px;font-weight:600;color:#1a1a1a;margin-bottom:8px}
.err{color:#d32f2f;font-size:14px;background:#fbeded;padding:12px 16px;border-radius:8px;border:1px solid #f5c6cb;margin-top:12px}
</style></head>
<body><div class="card">
<div class="icon"><svg viewBox="0 0 24 24"><path d="M18 6L6 18M6 6l12 12"/></svg></div>
<h1>Verification Failed</h1>
<p class="err">{{ERROR}}</p>
</div></body></html>`

func init() {
	keystoreRecoverCmd.Flags().String("id", "", "Key ID to recover (required)")
	keystoreRecoverCmd.Flags().String("output", "", "Output file path (default: key_<id>.pem)")
	keystoreCmd.AddCommand(keystoreRecoverCmd)
}
