package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("https://beta-api.pyxcloud.io", "pyxc_test123")
	if c.BaseURL != "https://beta-api.pyxcloud.io" {
		t.Errorf("BaseURL: got %q, want %q", c.BaseURL, "https://beta-api.pyxcloud.io")
	}
	if c.Token != "pyxc_test123" {
		t.Errorf("Token: got %q, want %q", c.Token, "pyxc_test123")
	}
}

func TestDoRequestSendsAuthHeader(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_mytoken")
	_, _, err := c.DoRequest("GET", "/test", nil)
	if err != nil {
		t.Fatalf("DoRequest failed: %v", err)
	}
	if capturedAuth != "Bearer pyxc_mytoken" {
		t.Errorf("Auth header: got %q, want %q", capturedAuth, "Bearer pyxc_mytoken")
	}
}

func TestDoRequestPOSTSendsJson(t *testing.T) {
	var capturedContentType string
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"received": "yes"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"name": "test-token"}
	_, _, err := c.DoRequest("POST", "/cli/token", body)
	if err != nil {
		t.Fatalf("DoRequest POST failed: %v", err)
	}
	if capturedContentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", capturedContentType, "application/json")
	}
	if capturedBody["name"] != "test-token" {
		t.Errorf("Body name: got %q, want %q", capturedBody["name"], "test-token")
	}
}

func TestProjectsReturnsSlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": 1, "name": "my-project"},
			{"id": 2, "name": "other-project"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	projects, err := c.Projects()
	if err != nil {
		t.Fatalf("Projects failed: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("Expected 2 projects, got %d", len(projects))
	}
	if projects[0]["name"] != "my-project" {
		t.Errorf("Project name: got %q, want %q", projects[0]["name"], "my-project")
	}
}

func TestDoRequestReturnsHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "forbidden"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_bad")
	_, status, err := c.DoRequest("GET", "/test", nil)
	if err != nil {
		t.Fatalf("DoRequest should not error on 403: %v", err)
	}
	if status != 403 {
		t.Errorf("Status: got %d, want %d", status, 403)
	}
}

func TestCompareReturnsMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"virtual-machine": []interface{}{
				map[string]interface{}{
					"headers": []interface{}{"Europe", map[string]interface{}{"name": "aws"}},
					"rows":    []interface{}{},
					"total":   0,
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	data, err := c.Compare("1", "0.1.0", "virtual-machine")
	if err != nil {
		t.Fatalf("Compare failed: %v", err)
	}
	if _, ok := data["virtual-machine"]; !ok {
		t.Error("Expected 'virtual-machine' key in compare response")
	}
}

func TestCompareOmitsTableIdWhenEmpty(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"auto": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	_, err := c.Compare("3", "0.1.0", "")
	if err != nil {
		t.Fatalf("Compare with empty tableId failed: %v", err)
	}
	if capturedPath != "/cli/compare/3/0.1.0" {
		t.Errorf("Expected no tableId in URL, got path: %q", capturedPath)
	}
}

func TestDeployInlineSendsCredentials(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":            "UNDER_DEPLOYMENT",
			"message":           "Inline deploy triggered successfully",
			"ephemeralTargetId": 99,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	creds := map[string]interface{}{
		"target": map[string]interface{}{
			"csp":     "aws",
			"account": `{"access_key_id":"AKIA","secret_access_key":"secret"}`,
		},
	}
	result, err := c.DeployInline("3", "0.1.0", creds)
	if err != nil {
		t.Fatalf("DeployInline failed: %v", err)
	}
	if capturedPath != "/cli/deploy/3/0.1.0/inline" {
		t.Errorf("Path: got %q, want %q", capturedPath, "/cli/deploy/3/0.1.0/inline")
	}
	if result["status"] != "UNDER_DEPLOYMENT" {
		t.Errorf("Status: got %q", result["status"])
	}
	target := capturedBody["target"].(map[string]interface{})
	if target["csp"] != "aws" {
		t.Errorf("Body target.csp: got %q", target["csp"])
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Keystore
// ═══════════════════════════════════════════════════════════════════════

func TestKeystoreListReturnsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/keystore" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": 1, "label": "prod-key", "status": "active", "projectName": "My Project"},
			{"id": 2, "label": "dev-key", "status": "active"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	keys, err := c.KeystoreList()
	if err != nil {
		t.Fatalf("KeystoreList failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}
	if keys[0]["label"] != "prod-key" {
		t.Errorf("Key label: got %q", keys[0]["label"])
	}
}

func TestKeystoreCreateSendsLabel(t *testing.T) {
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/keystore" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 3, "label": "new-key", "publicKey": "ssh-rsa AAAA...",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"label": "new-key"}
	result, err := c.KeystoreCreate(body)
	if err != nil {
		t.Fatalf("KeystoreCreate failed: %v", err)
	}
	if capturedBody["label"] != "new-key" {
		t.Errorf("Body label: got %q", capturedBody["label"])
	}
	if result["label"] != "new-key" {
		t.Errorf("Result label: got %q", result["label"])
	}
}

func TestKeystoreDeleteSendsCorrectPath(t *testing.T) {
	var capturedPath, capturedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	err := c.KeystoreDelete("42")
	if err != nil {
		t.Fatalf("KeystoreDelete failed: %v", err)
	}
	if capturedMethod != "DELETE" {
		t.Errorf("Method: got %q, want DELETE", capturedMethod)
	}
	if capturedPath != "/keystore/42" {
		t.Errorf("Path: got %q, want /keystore/42", capturedPath)
	}
}

func TestKeystoreDeleteReturnsErrorOnNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	err := c.KeystoreDelete("999")
	if err == nil {
		t.Error("Expected error on 404")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Settings / RBAC
// ═══════════════════════════════════════════════════════════════════════

func TestSettingsMeReturnsIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rbac/me" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "Test User", "email": "test@example.com",
			"isAdmin": true, "roles": []string{"pyx-admin-role"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	me, err := c.SettingsMe()
	if err != nil {
		t.Fatalf("SettingsMe failed: %v", err)
	}
	if me["name"] != "Test User" {
		t.Errorf("Name: got %q", me["name"])
	}
	if me["isAdmin"] != true {
		t.Errorf("isAdmin: got %v", me["isAdmin"])
	}
}

func TestSettingsTeamListsUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rbac/org/users" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "u1", "name": "Alice", "email": "alice@example.com", "roles": []string{"pyx-admin-role"}},
			{"id": "u2", "name": "Bob", "email": "bob@example.com", "roles": []string{"pyx-developer-role"}},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	users, err := c.SettingsUsers()
	if err != nil {
		t.Fatalf("SettingsUsers failed: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("Expected 2 users, got %d", len(users))
	}
}

func TestSettingsSeatsReturnsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rbac/org/seats" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"used": 3, "max": 10, "remaining": 7,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	seats, err := c.SettingsSeats()
	if err != nil {
		t.Fatalf("SettingsSeats failed: %v", err)
	}
	if seats["max"] != float64(10) {
		t.Errorf("Max: got %v", seats["max"])
	}
}

func TestSettingsInviteSendsEmail(t *testing.T) {
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/rbac/invite" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"message": "invited"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"email": "new@example.com"}
	_, err := c.SettingsInvite(body)
	if err != nil {
		t.Fatalf("SettingsInvite failed: %v", err)
	}
	if capturedBody["email"] != "new@example.com" {
		t.Errorf("Body email: got %q", capturedBody["email"])
	}
}

func TestSettingsAssignRoleSendsPayload(t *testing.T) {
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/rbac/assign" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"message": "assigned"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"userId": "u1", "roleName": "pyx-developer-role"}
	_, err := c.SettingsAssignRole(body)
	if err != nil {
		t.Fatalf("SettingsAssignRole failed: %v", err)
	}
	if capturedBody["userId"] != "u1" {
		t.Errorf("Body userId: got %q", capturedBody["userId"])
	}
	if capturedBody["roleName"] != "pyx-developer-role" {
		t.Errorf("Body roleName: got %q", capturedBody["roleName"])
	}
}

func TestSettingsRemoveRoleSendsPayload(t *testing.T) {
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/rbac/remove" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"message": "removed"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"userId": "u1", "roleName": "pyx-developer-role"}
	_, err := c.SettingsRemoveRole(body)
	if err != nil {
		t.Fatalf("SettingsRemoveRole failed: %v", err)
	}
	if capturedBody["roleName"] != "pyx-developer-role" {
		t.Errorf("Body roleName: got %q", capturedBody["roleName"])
	}
}

// ═══════════════════════════════════════════════════════════════════════
// CLI Tokens
// ═══════════════════════════════════════════════════════════════════════

func TestTokenListReturnsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/token" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": 1, "name": "ci-token", "createdAt": "2026-01-01"},
			{"id": 2, "name": "dev-token", "createdAt": "2026-02-01", "lastUsedAt": "2026-03-01"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	tokens, err := c.TokenList()
	if err != nil {
		t.Fatalf("TokenList failed: %v", err)
	}
	if len(tokens) != 2 {
		t.Errorf("Expected 2 tokens, got %d", len(tokens))
	}
}

func TestTokenCreateSendsNameAndReturnsToken(t *testing.T) {
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/cli/token" {
			t.Errorf("Unexpected %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 3, "name": "new-token", "token": "pyxc_secret_value",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	body := map[string]string{"name": "new-token"}
	result, err := c.TokenCreate(body)
	if err != nil {
		t.Fatalf("TokenCreate failed: %v", err)
	}
	if capturedBody["name"] != "new-token" {
		t.Errorf("Body name: got %q", capturedBody["name"])
	}
	if result["token"] != "pyxc_secret_value" {
		t.Errorf("Token: got %q", result["token"])
	}
}

func TestTokenRevokeSendsDelete(t *testing.T) {
	var capturedPath, capturedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	err := c.TokenRevoke("7")
	if err != nil {
		t.Fatalf("TokenRevoke failed: %v", err)
	}
	if capturedMethod != "DELETE" {
		t.Errorf("Method: got %q, want DELETE", capturedMethod)
	}
	if capturedPath != "/cli/token/7" {
		t.Errorf("Path: got %q, want /cli/token/7", capturedPath)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Key Recovery (Half-A, Half-B, StepUp)
// ═══════════════════════════════════════════════════════════════════════

func TestKeystoreHalfAReturnsShare(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/keystore/5/half-a" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"halfA": "801abc123def456",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	result, err := c.KeystoreHalfA("5")
	if err != nil {
		t.Fatalf("KeystoreHalfA failed: %v", err)
	}
	if result["halfA"] != "801abc123def456" {
		t.Errorf("halfA: got %q", result["halfA"])
	}
}

func TestKeystoreHalfBSendsStepUpHeader(t *testing.T) {
	var capturedStepUp string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/keystore/5/half-b" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		capturedStepUp = r.Header.Get("X-StepUp-Token")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"halfB": "802def789abc012",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pyxc_test")
	result, err := c.KeystoreHalfB("5", "step-up-token-xyz")
	if err != nil {
		t.Fatalf("KeystoreHalfB failed: %v", err)
	}
	if capturedStepUp != "step-up-token-xyz" {
		t.Errorf("X-StepUp-Token: got %q, want %q", capturedStepUp, "step-up-token-xyz")
	}
	if result["halfB"] != "802def789abc012" {
		t.Errorf("halfB: got %q", result["halfB"])
	}
}
