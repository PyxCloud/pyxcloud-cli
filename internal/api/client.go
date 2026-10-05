package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// notAvailable marks operations whose legacy /cli/* endpoint has no governed
// /vibe equivalent yet (mapping per ops/deliverables/recon/r1-cli-client-vs-allowlist.md).
// The cobra commands stay in place; they fail with this honest error instead of
// silently hitting a removed legacy route.
func notAvailable(op string) error {
	return fmt.Errorf("%s is not available on the governed API yet (no /vibe equivalent)", op)
}

const (
	contentTypeJSON   = "application/json"
	errAuthRefreshFmt = "auth refresh: %w"
	errFailedHTTPFmt  = "failed (HTTP %d): %s"
)

// Client talks to the PyxCloud backend CLI API.
type Client struct {
	BaseURL      string
	Token        string // current access_token
	RefreshToken string // offline refresh_token (may be empty for PAT-only)
	AuthURL      string // Keycloak token endpoint base (e.g., https://beta-auth.pyxcloud.io/realms/pyx)
	ClientID     string // OAuth2 client ID
	HTTPClient   *http.Client
}

// NewClient creates a client from config.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientFromConfig creates a client with full refresh support.
func NewClientFromConfig(cfg *config.Config) *Client {
	return &Client{
		BaseURL:      cfg.APIURL,
		Token:        cfg.Token,
		RefreshToken: cfg.RefreshToken,
		AuthURL:      cfg.AuthURL,
		ClientID:     cfg.ClientID,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ensureToken keeps the saved access token usable. The governed /vibe API has
// no PAT-exchange endpoint (the legacy POST /cli/refresh is not in the
// allowlist): tokens come from the OAuth2.1+PKCE browser login
// (cmd/auth.go loginWithBrowser). When only an offline refresh_token is
// stored, the token is renewed directly against Keycloak with the standard
// refresh_token grant — no new backend endpoint is involved.
func (c *Client) ensureToken() error {
	if c.Token != "" {
		return nil // token as-is
	}
	if c.RefreshToken == "" || c.AuthURL == "" {
		return fmt.Errorf("no saved token: run `pyx auth login` first")
	}

	tokenEndpoint := c.AuthURL + "/protocol/openid-connect/token"
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", c.ClientID)
	data.Set("refresh_token", c.RefreshToken)

	resp, err := c.HTTPClient.PostForm(tokenEndpoint, data)
	if err != nil {
		return fmt.Errorf(errAuthRefreshFmt, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("auth refresh: token refresh returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("auth refresh: decode response: %w", err)
	}

	accessToken, ok := result["access_token"].(string)
	if !ok || accessToken == "" {
		return fmt.Errorf("auth refresh: no access_token in response")
	}
	c.Token = accessToken
	return nil
}

// DoRequest performs an authenticated HTTP request.
func (c *Client) DoRequest(method, path string, body interface{}) ([]byte, int, error) {
	// Auto-refresh token before each call
	if err := c.ensureToken(); err != nil {
		return nil, 0, fmt.Errorf(errAuthRefreshFmt, err)
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return data, resp.StatusCode, nil
}

// Auth validates the token. The legacy POST /cli/auth probe has no governed
// equivalent; the closest allowed governed read is GET /vibe/rbac/credentials
// (it succeeds only with a valid JWT). The governed endpoint returns a
// credentials list; Auth surfaces the caller's own first entry as the
// validation result.
func (c *Client) Auth() (map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/vibe/rbac/credentials", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("authentication failed (HTTP %d): %s", status, string(data))
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		var single map[string]interface{}
		return single, json.Unmarshal(data, &single)
	}
	if len(list) == 0 {
		return map[string]interface{}{}, nil
	}
	return list[0], nil
}

// Projects lists all projects.
func (c *Client) Projects() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/vibe/projects", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf(errFailedHTTPFmt, status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Builds has no governed equivalent: the /vibe flow is state-machine-centric
// (project journey / releases), not build-centric.
func (c *Client) Builds(projectID string) ([]map[string]interface{}, error) {
	_ = projectID
	return nil, notAvailable("builds")
}

// Compare gets the governed region-compare workspace for a project version
// (CLD-01/CLD-02, allowlist.js: GET .../versions/:versionId/cloud/compare).
// The legacy caller passed a buildVersion; on the governed API the same slot
// is the project version id. tableId has no governed equivalent and is
// ignored (kept in the signature so cobra flags stay compatible).
func (c *Client) Compare(projectID, buildVersion, tableId string) (map[string]interface{}, error) {
	_ = tableId
	path := fmt.Sprintf("/vibe/projects/%s/versions/%s/cloud/compare", projectID, buildVersion)
	data, status, err := c.DoRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf(errFailedHTTPFmt, status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Deploy triggers deployment. The governed state-machine write is
// POST /vibe/projects/:projectId/redeploy (allowlist.js; the release is
// derived from project state, not from a buildVersion path segment).
func (c *Client) Deploy(projectID, buildVersion string) (map[string]interface{}, error) {
	_ = buildVersion
	path := fmt.Sprintf("/vibe/projects/%s/redeploy", projectID)
	data, status, err := c.DoRequest("POST", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("deploy failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// DeployInline has no governed equivalent: /vibe deploys run on account
// bindings registered server-side (/vibe/accountbinding), not inline
// credentials sent with the deploy call.
func (c *Client) DeployInline(projectID, buildVersion string, credentials interface{}) (map[string]interface{}, error) {
	_ = projectID
	_ = buildVersion
	_ = credentials
	return nil, notAvailable("inline deploy")
}

// Status reads the governed journey read model (X-01, allowlist.js:
// GET /vibe/projects/:projectId/journey). The legacy caller passed a
// buildVersion; on the governed API the same slot is the project id.
func (c *Client) Status(projectID, buildVersion string) (map[string]interface{}, error) {
	_ = buildVersion
	path := fmt.Sprintf("/vibe/projects/%s/journey", projectID)
	data, status, err := c.DoRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf(errFailedHTTPFmt, status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Destroy retires the project lifecycle (mapping decision "destroy →
// lifecycle retire").
func (c *Client) Destroy(projectID string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/vibe/projects/%s/lifecycle/retire", projectID)
	data, status, err := c.DoRequest("POST", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("destroy failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ── Keystore ────────────────────────────────────────────────────────────

// KeystoreList lists all SSH key associations.
func (c *Client) KeystoreList() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/keystore", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("keystore list failed (HTTP %d): %s", status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// KeystoreCreate creates a new key association.
func (c *Client) KeystoreCreate(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/keystore", body)
	if err != nil {
		return nil, err
	}
	if status != 200 && status != 201 {
		return nil, fmt.Errorf("keystore create failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// KeystoreDelete deletes a key association by ID.
func (c *Client) KeystoreDelete(keyID string) error {
	data, status, err := c.DoRequest("DELETE", "/keystore/"+keyID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("keystore delete failed (HTTP %d): %s", status, string(data))
	}
	return nil
}

// ── Settings / RBAC ─────────────────────────────────────────────────────

// SettingsMe returns the current user's identity and roles.
func (c *Client) SettingsMe() (map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/rbac/me", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("settings me failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// SettingsUsers lists org users (admin only).
func (c *Client) SettingsUsers() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/rbac/org/users", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("users list failed (HTTP %d): %s", status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// SettingsSeats returns seat usage info.
func (c *Client) SettingsSeats() (map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/rbac/org/seats", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("seats failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// SettingsInvite sends an invitation to a user.
func (c *Client) SettingsInvite(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/rbac/invite", body)
	if err != nil {
		return nil, err
	}
	if status != 200 && status != 201 {
		return nil, fmt.Errorf("invite failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// SettingsAssignRole assigns a role to a user.
func (c *Client) SettingsAssignRole(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/rbac/assign", body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("assign role failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// SettingsRemoveRole removes a role from a user.
func (c *Client) SettingsRemoveRole(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("DELETE", "/rbac/remove", body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("remove role failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ── CLI Tokens ──────────────────────────────────────────────────────────

// TokenList lists the governed stored credentials
// (allowlist.js: GET /vibe/rbac/credentials).
func (c *Client) TokenList() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/vibe/rbac/credentials", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("token list failed (HTTP %d): %s", status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// TokenCreate has no governed equivalent: the allowlist exposes only the
// read GET /vibe/rbac/credentials (credentials are managed server-side).
func (c *Client) TokenCreate(body interface{}) (map[string]interface{}, error) {
	_ = body
	return nil, notAvailable("token create")
}

// TokenRevoke has no governed equivalent: the allowlist exposes only the
// read GET /vibe/rbac/credentials (credentials are managed server-side).
func (c *Client) TokenRevoke(tokenID string) error {
	_ = tokenID
	return notAvailable("token revoke")
}

// ── Key Recovery ────────────────────────────────────────────────────────

// DoRequestWithHeaders performs an authenticated HTTP request with extra headers.
func (c *Client) DoRequestWithHeaders(method, path string, body interface{}, extraHeaders map[string]string) ([]byte, int, error) {
	if err := c.ensureToken(); err != nil {
		return nil, 0, fmt.Errorf(errAuthRefreshFmt, err)
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return data, resp.StatusCode, nil
}

// KeystoreHalfA retrieves the system Shamir share (Half-A) for a key.
func (c *Client) KeystoreHalfA(keyID string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/keystore/%s/half-a", keyID)
	data, status, err := c.DoRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("half-a retrieval failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// KeystoreHalfB retrieves the recovery Shamir share (Half-B) from Vault.
// Requires a step-up token obtained via browser-based WebAuthn verification.
func (c *Client) KeystoreHalfB(keyID, stepUpToken string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/keystore/%s/half-b", keyID)
	headers := map[string]string{"X-StepUp-Token": stepUpToken}
	data, status, err := c.DoRequestWithHeaders("GET", path, nil, headers)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("half-b retrieval failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ── Project CRUD ────────────────────────────────────────────────────────

// ProjectCreate creates a new project.
func (c *Client) ProjectCreate(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/vibe/projects", body)
	if err != nil {
		return nil, err
	}
	if status != 200 && status != 201 {
		return nil, fmt.Errorf("project create failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ProjectDelete deletes a project by ID.
func (c *Client) ProjectDelete(projectID string) error {
	data, status, err := c.DoRequest("DELETE", "/vibe/projects/"+projectID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("project delete failed (HTTP %d): %s", status, string(data))
	}
	return nil
}

// ── Account Binding CRUD ────────────────────────────────────────────────

// AccountList lists all account bindings
// (allowlist.js: GET /vibe/accountbinding).
func (c *Client) AccountList() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/vibe/accountbinding", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("account list failed (HTTP %d): %s", status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// AccountCreate creates a new account binding.
func (c *Client) AccountCreate(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/vibe/accountbinding", body)
	if err != nil {
		return nil, err
	}
	if status != 200 && status != 201 {
		return nil, fmt.Errorf("account create failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// AccountDelete deletes an account binding by ID.
func (c *Client) AccountDelete(accountID string) error {
	data, status, err := c.DoRequest("DELETE", "/vibe/accountbinding/"+accountID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("account delete failed (HTTP %d): %s", status, string(data))
	}
	return nil
}

// AccountVerify has no governed equivalent in the allowlist (account
// bindings are verified server-side on POST /vibe/accountbinding).
func (c *Client) AccountVerify(accountID string) (map[string]interface{}, error) {
	_ = accountID
	return nil, notAvailable("account verify")
}

// ── Import Workflow ─────────────────────────────────────────────────────

// ImportDiscover has no governed equivalent: /cli/import/* is legacy-only.
func (c *Client) ImportDiscover(accountID string) (map[string]interface{}, error) {
	_ = accountID
	return nil, notAvailable("import discover")
}

// ImportBuild has no governed equivalent: /cli/import/* is legacy-only.
func (c *Client) ImportBuild(projectID, accountID string, selectedIDs []string) (map[string]interface{}, error) {
	_ = projectID
	_ = accountID
	_ = selectedIDs
	return nil, notAvailable("import build")
}

// ── Local Deploy ────────────────────────────────────────────────────────

// DeployLocal has no governed equivalent yet: the governed local-run flow is
// the board local-feedback pipeline (/vibe/projects/:id/board/tasks/:taskId/local-feedback),
// which is keyed on a board task, not on a build version.
func (c *Client) DeployLocal(projectID, buildVersion string) (map[string]interface{}, error) {
	_ = projectID
	_ = buildVersion
	return nil, notAvailable("local deploy")
}

// DeployComplete has no governed equivalent yet: the governed local-run flow
// is the board local-feedback pipeline
// (/vibe/projects/:id/board/tasks/:taskId/local-feedback), which is keyed on a
// board task, not on a build version/execution id.
func (c *Client) DeployComplete(projectID, buildVersion, executionID, stepUpToken string, tfStates map[string]string) (map[string]interface{}, error) {
	_ = projectID
	_ = buildVersion
	_ = executionID
	_ = stepUpToken
	_ = tfStates
	return nil, notAvailable("deploy completion")
}

// DeepScanReport has no governed equivalent: /cli/import/* is legacy-only.
func (c *Client) DeepScanReport(token string, keys []string) error {
	_ = token
	_ = keys
	return notAvailable("scan report")
}

// DoRaw sends a request with a RAW (non-JSON-marshalled) body and an explicit
// Content-Type, going through the same token refresh as DoRequest. Used for
// endpoints that consume text/plain (e.g. the Pyxfile plan endpoint) or where
// the caller already has serialized bytes. extraHeaders override defaults.
func (c *Client) DoRaw(method, path string, body []byte, contentType string, extraHeaders map[string]string) ([]byte, int, error) {
	if err := c.ensureToken(); err != nil {
		return nil, 0, fmt.Errorf(errAuthRefreshFmt, err)
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", contentTypeJSON)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return data, resp.StatusCode, nil
}
