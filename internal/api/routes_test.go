// routes_test.go — table-driven check of the legacy /cli/* → governed /vibe/*
// route mapping decided in FASE C (spec: ops/deliverables/recon/
// r1-cli-client-vs-allowlist.md + the FASE C task contract).
//
// For each client operation the table records the HTTP method and path the
// client must emit against the governed API. Operations without a governed
// equivalent are asserted to fail honestly without touching the network.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type routeProbe struct {
	srv    *httptest.Server
	client *Client
	method string
	path   string
	hit    bool
}

func newRouteProbe(t *testing.T) *routeProbe {
	t.Helper()
	p := &routeProbe{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hit = true
		p.method = r.Method
		p.path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		// List-style governed endpoints return arrays; everything else an object.
		if r.Method == "GET" && (r.URL.Path == "/vibe/projects" || r.URL.Path == "/vibe/accountbinding" || r.URL.Path == "/vibe/rbac/credentials") {
			w.Write([]byte("[]"))
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	p.client = NewClient(p.srv.URL, "pyxc_test")
	return p
}

// routeCase asserts one governed route: name, the call, expected method+path.
type routeCase struct {
	name       string
	call       func(c *Client) error
	wantMethod string
	wantPath   string
}

func routeCases() []routeCase {
	return []routeCase{
		{
			name:       "Auth probes GET /vibe/rbac/credentials",
			call:       func(c *Client) error { _, err := c.Auth(); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/rbac/credentials",
		},
		{
			name:       "Projects lists GET /vibe/projects",
			call:       func(c *Client) error { _, err := c.Projects(); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/projects",
		},
		{
			name:       "ProjectCreate posts /vibe/projects",
			call:       func(c *Client) error { _, err := c.ProjectCreate(map[string]string{"name": "p"}); return err },
			wantMethod: "POST",
			wantPath:   "/vibe/projects",
		},
		{
			name:       "ProjectDelete deletes /vibe/projects/:projectId",
			call:       func(c *Client) error { return c.ProjectDelete("42") },
			wantMethod: "DELETE",
			wantPath:   "/vibe/projects/42",
		},
		{
			name:       "Compare reads governed cloud compare",
			call:       func(c *Client) error { _, err := c.Compare("42", "7", ""); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/projects/42/versions/7/cloud/compare",
		},
		{
			name:       "Deploy posts governed redeploy",
			call:       func(c *Client) error { _, err := c.Deploy("42", "0.1.0"); return err },
			wantMethod: "POST",
			wantPath:   "/vibe/projects/42/redeploy",
		},
		{
			name:       "Status reads governed journey",
			call:       func(c *Client) error { _, err := c.Status("42", "0.1.0"); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/projects/42/journey",
		},
		{
			name:       "Destroy posts governed lifecycle retire",
			call:       func(c *Client) error { _, err := c.Destroy("42"); return err },
			wantMethod: "POST",
			wantPath:   "/vibe/projects/42/lifecycle/retire",
		},
		{
			name:       "TokenList reads governed credentials",
			call:       func(c *Client) error { _, err := c.TokenList(); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/rbac/credentials",
		},
		{
			name:       "AccountList reads governed accountbinding",
			call:       func(c *Client) error { _, err := c.AccountList(); return err },
			wantMethod: "GET",
			wantPath:   "/vibe/accountbinding",
		},
		{
			name:       "AccountCreate posts governed accountbinding",
			call:       func(c *Client) error { _, err := c.AccountCreate(map[string]string{"csp": "aws"}); return err },
			wantMethod: "POST",
			wantPath:   "/vibe/accountbinding",
		},
		{
			name:       "AccountDelete deletes governed accountbinding/:bindingId",
			call:       func(c *Client) error { return c.AccountDelete("9") },
			wantMethod: "DELETE",
			wantPath:   "/vibe/accountbinding/9",
		},
	}
}

func TestGovernedRouteMapping(t *testing.T) {
	for _, tc := range routeCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := newRouteProbe(t)
			defer p.srv.Close()

			if err := tc.call(p.client); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if !p.hit {
				t.Fatal("expected an HTTP request against the governed route")
			}
			if p.method != tc.wantMethod {
				t.Errorf("method: got %q, want %q", p.method, tc.wantMethod)
			}
			if p.path != tc.wantPath {
				t.Errorf("path: got %q, want %q", p.path, tc.wantPath)
			}
		})
	}
}

// TestLegacyOnlyOperationsFailHonest asserts the no-equivalent operations fail
// with the explicit "not available on governed API yet" error and never hit
// the network (the server in the probe is deliberately unreachable: an
// unexpected HTTP call would surface as a dial error wrapped into the
// returned message, not the not-available sentinel).
func TestLegacyOnlyOperationsFailHonest(t *testing.T) {
	unreachable := NewClient("http://127.0.0.1:1", "pyxc_test")
	cases := []struct {
		name string
		call func() error
	}{
		{"Builds", func() error { _, err := unreachable.Builds("42"); return err }},
		{"DeployInline", func() error { _, err := unreachable.DeployInline("42", "0.1.0", nil); return err }},
		{"DeployLocal", func() error { _, err := unreachable.DeployLocal("42", "0.1.0"); return err }},
		{"DeployComplete", func() error { _, err := unreachable.DeployComplete("42", "0.1.0", "e1", "s", nil); return err }},
		{"ImportDiscover", func() error { _, err := unreachable.ImportDiscover("acc"); return err }},
		{"ImportBuild", func() error { _, err := unreachable.ImportBuild("42", "acc", nil); return err }},
		{"DeepScanReport", func() error { return unreachable.DeepScanReport("t", nil) }},
		{"TokenCreate", func() error { _, err := unreachable.TokenCreate(nil); return err }},
		{"TokenRevoke", func() error { return unreachable.TokenRevoke("7") }},
		{"AccountVerify", func() error { _, err := unreachable.AccountVerify("9"); return err }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s should fail honestly, got nil error", tc.name)
			}
			if !strings.Contains(err.Error(), "is not available on the governed API yet") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
