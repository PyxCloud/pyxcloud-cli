package passoauth

import (
	"testing"
)

func TestResolveProfileSandboxDefaults(t *testing.T) {
	clearProfileEnv(t)
	got, err := ResolveProfile("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	want := Profile{Name: "sandbox", APIURL: "http://127.0.0.1:16080", IssuerURL: "http://sso.localtest.me:18081/realms/passobuild", ClientID: "passo-cli", ConsoleURL: "http://127.0.0.1:13000"}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestResolveProfileStagingRequiresExplicitEndpoints(t *testing.T) {
	clearProfileEnv(t)
	if _, err := ResolveProfile("staging"); err == nil {
		t.Fatal("expected missing staging endpoints to fail")
	}
}

func TestResolveProfileEnvironmentOverridesBothProfiles(t *testing.T) {
	clearProfileEnv(t)
	t.Setenv("PASSO_API_URL", "https://api.example.test/v1")
	t.Setenv("PASSO_ISSUER_URL", "https://id.example.test/realm")
	t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
	t.Setenv("PASSO_CLIENT_ID", "custom-cli")
	for _, name := range []string{"sandbox", "staging"} {
		got, err := ResolveProfile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.APIURL != "https://api.example.test/v1" || got.IssuerURL != "https://id.example.test/realm" || got.ConsoleURL != "https://console.example.test" || got.ClientID != "custom-cli" {
			t.Fatalf("overrides not applied for %s: %#v", name, got)
		}
	}
}

func TestValidateProfileRejectsUnsafeURLs(t *testing.T) {
	base := Profile{Name: "sandbox", APIURL: "http://127.0.0.1:16080", IssuerURL: "http://127.0.0.1:18081/realm", ClientID: "passo-cli", ConsoleURL: "http://127.0.0.1:13000"}
	cases := []struct{ name, field, value string }{
		{"userinfo", "api", "https://user:pass@example.test"},
		{"query", "api", "https://example.test?x=1"},
		{"fragment", "issuer", "https://example.test#frag"},
		{"empty-query", "api", "https://example.test?"},
		{"empty-fragment", "issuer", "https://example.test#"},
		{"scheme", "console", "ftp://example.test"},
		{"remote-http", "api", "http://api.example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			switch tc.field {
			case "api":
				p.APIURL = tc.value
			case "issuer":
				p.IssuerURL = tc.value
			case "console":
				p.ConsoleURL = tc.value
			}
			if err := ValidateProfile(p); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestResolveProfileRejectsUnknownName(t *testing.T) {
	clearProfileEnv(t)
	if _, err := ResolveProfile("production"); err == nil {
		t.Fatal("expected unknown profile error")
	}
}

func clearProfileEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PASSO_API_URL", "PASSO_ISSUER_URL", "PASSO_CONSOLE_URL", "PASSO_CLIENT_ID"} {
		t.Setenv(key, "")
	}
}

func TestValidateProfileAllowsOnlySandboxSSOHTTPHost(t *testing.T) {
	p := Profile{Name: "sandbox", APIURL: "http://sso.localtest.me:18081", IssuerURL: "http://sso.localtest.me:18081/realms/passobuild", ClientID: "passo-cli", ConsoleURL: "http://127.0.0.1:13000"}
	if err := ValidateProfile(p); err != nil {
		t.Fatalf("sandbox SSO hostname should be allowed: %v", err)
	}
	p.APIURL = "http://other.localtest.me:18081"
	if err := ValidateProfile(p); err == nil {
		t.Fatal("expected non-exact localtest.me host to fail")
	}
	p.Name = "staging"
	p.APIURL = "http://sso.localtest.me:18081"
	if err := ValidateProfile(p); err == nil {
		t.Fatal("staging must reject sandbox SSO HTTP exception")
	}
}
