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
	AuthURL      string // Keycloak token endpoint base (e.g., http://localhost:8180/realms/pyx)
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

// ensureToken exchanges the stored PAT for a short-lived access JWT
// via the backend's /cli/refresh endpoint. Called before every API request.
func (c *Client) ensureToken() error {
	if c.RefreshToken == "" {
		return nil // direct JWT mode — use token as-is
	}

	refreshURL := c.BaseURL + "/cli/refresh"
	payload, _ := json.Marshal(map[string]string{"pat": c.RefreshToken})

	resp, err := c.HTTPClient.Post(refreshURL, contentTypeJSON, bytes.NewReader(payload))
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

// Auth validates the token.
func (c *Client) Auth() (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/cli/auth", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("authentication failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Projects lists all projects.
func (c *Client) Projects() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/cli/projects", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf(errFailedHTTPFmt, status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Builds lists builds for a project.
func (c *Client) Builds(projectID string) ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/cli/builds/"+projectID, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf(errFailedHTTPFmt, status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Compare gets comparison table data.
func (c *Client) Compare(projectID, buildVersion, tableId string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/cli/compare/%s/%s", projectID, buildVersion)
	if tableId != "" {
		path += "?tableId=" + url.QueryEscape(tableId)
	}
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

// Deploy triggers deployment.
func (c *Client) Deploy(projectID, buildVersion string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/cli/deploy/%s/%s", projectID, buildVersion)
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

// DeployInline triggers deployment with inline credentials.
func (c *Client) DeployInline(projectID, buildVersion string, credentials interface{}) (map[string]interface{}, error) {
	path := fmt.Sprintf("/cli/deploy/%s/%s/inline", projectID, buildVersion)
	data, status, err := c.DoRequest("POST", path, credentials)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("inline deploy failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Status checks deploy status.
func (c *Client) Status(projectID, buildVersion string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/cli/status/%s/%s", projectID, buildVersion)
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

// Destroy triggers infrastructure destruction.
func (c *Client) Destroy(projectID string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/cli/destroy/%s", projectID)
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

// TokenList lists CLI API tokens.
func (c *Client) TokenList() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/cli/token", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("token list failed (HTTP %d): %s", status, string(data))
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// TokenCreate creates a new CLI API token.
func (c *Client) TokenCreate(body interface{}) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/cli/token", body)
	if err != nil {
		return nil, err
	}
	if status != 200 && status != 201 {
		return nil, fmt.Errorf("token create failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// TokenRevoke revokes a CLI API token.
func (c *Client) TokenRevoke(tokenID string) error {
	data, status, err := c.DoRequest("DELETE", "/cli/token/"+tokenID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("token revoke failed (HTTP %d): %s", status, string(data))
	}
	return nil
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
	data, status, err := c.DoRequest("POST", "/cli/projects", body)
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
	data, status, err := c.DoRequest("DELETE", "/cli/projects/"+projectID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("project delete failed (HTTP %d): %s", status, string(data))
	}
	return nil
}

// ── Account Binding CRUD ────────────────────────────────────────────────

// AccountList lists all account bindings.
func (c *Client) AccountList() ([]map[string]interface{}, error) {
	data, status, err := c.DoRequest("GET", "/cli/accounts", nil)
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
	data, status, err := c.DoRequest("POST", "/cli/accounts", body)
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
	data, status, err := c.DoRequest("DELETE", "/cli/accounts/"+accountID, nil)
	if err != nil {
		return err
	}
	if status != 200 && status != 204 {
		return fmt.Errorf("account delete failed (HTTP %d): %s", status, string(data))
	}
	return nil
}

// AccountVerify verifies account binding credentials.
func (c *Client) AccountVerify(accountID string) (map[string]interface{}, error) {
	data, status, err := c.DoRequest("POST", "/cli/accounts/"+accountID+"/verify", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("account verify failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ── Import Workflow ─────────────────────────────────────────────────────

// ImportDiscover discovers cloud resources from an account binding.
func (c *Client) ImportDiscover(accountID string) (map[string]interface{}, error) {
	body := map[string]interface{}{"accountId": accountID}
	data, status, err := c.DoRequest("POST", "/cli/import/discover", body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("import discover failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ImportBuild creates a Build from imported resources.
func (c *Client) ImportBuild(projectID, accountID string, selectedIDs []string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"projectId":   projectID,
		"accountId":   accountID,
		"selectedIds": selectedIDs,
	}
	data, status, err := c.DoRequest("POST", "/cli/import/build", body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("import build failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ── Local Deploy ────────────────────────────────────────────────────────

// DeployLocal gets the local deploy bash scripts and configuration.
func (c *Client) DeployLocal(projectID, buildVersion string) (map[string]interface{}, error) {
	url := fmt.Sprintf("/cli/deploy/%s/%s/local", projectID, buildVersion)
	data, status, err := c.DoRequest("POST", url, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("local deploy generation failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// DeployComplete pushes the final tfstate back to the backend.
// Requires a step-up token for MFA verification.
func (c *Client) DeployComplete(projectID, buildVersion, executionID, stepUpToken string, tfStates map[string]string) (map[string]interface{}, error) {
	url := fmt.Sprintf("/cli/deploy/%s/%s/complete", projectID, buildVersion)
	body := map[string]interface{}{
		"executionId": executionID,
		"tfstates":    tfStates,
	}

	headers := map[string]string{
		"X-StepUp-Token": stepUpToken,
	}
	data, status, err := c.DoRequestWithHeaders("POST", url, body, headers)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("deploy completion failed (HTTP %d): %s", status, string(data))
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}
