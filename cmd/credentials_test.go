package cmd

import (
	"encoding/json"
	"os"
	"testing"
)

func TestResolveCredentialsFromFlag(t *testing.T) {
	root := deployCmd
	root.ResetFlags()
	root.Flags().String("credentials", "", "")
	root.Flags().String("credentials-file", "", "")
	root.Flags().Bool("non-interactive", false, "")

	creds := `{"target":{"csp":"aws","account":"{\"access_key_id\":\"AKIA...\",\"secret_access_key\":\"secret\"}"}}`
	root.Flags().Set("credentials", creds)

	payload, err := resolveCredentials(root)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}
	if payload == nil {
		t.Fatal("Expected non-nil payload")
	}
	if payload.Target.Csp != "aws" {
		t.Errorf("Target.Csp: got %q, want %q", payload.Target.Csp, "aws")
	}
}

func TestResolveCredentialsFromFile(t *testing.T) {
	f, err := os.CreateTemp("", "creds-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	credsJSON := `{"target":{"csp":"gcp","account":"{\"project_id\":\"my-proj\"}"}}`
	f.WriteString(credsJSON)
	f.Close()

	root := deployCmd
	root.ResetFlags()
	root.Flags().String("credentials", "", "")
	root.Flags().String("credentials-file", "", "")
	root.Flags().Bool("non-interactive", false, "")

	root.Flags().Set("credentials-file", f.Name())

	payload, err := resolveCredentials(root)
	if err != nil {
		t.Fatalf("resolveCredentials from file failed: %v", err)
	}
	if payload == nil {
		t.Fatal("Expected non-nil payload from file")
	}
	if payload.Target.Csp != "gcp" {
		t.Errorf("Target.Csp: got %q, want %q", payload.Target.Csp, "gcp")
	}
}

func TestResolveCredentialsFromEnv(t *testing.T) {
	root := deployCmd
	root.ResetFlags()
	root.Flags().String("credentials", "", "")
	root.Flags().String("credentials-file", "", "")
	root.Flags().Bool("non-interactive", false, "")

	os.Setenv("PYXCLOUD_CREDENTIALS", `{"target":{"csp":"linode","account":"{\"token\":\"123\"}"}}`)
	defer os.Unsetenv("PYXCLOUD_CREDENTIALS")

	payload, err := resolveCredentials(root)
	if err != nil {
		t.Fatalf("resolveCredentials from env failed: %v", err)
	}
	if payload == nil {
		t.Fatal("Expected non-nil payload from env")
	}
	if payload.Target.Csp != "linode" {
		t.Errorf("Target.Csp: got %q, want %q", payload.Target.Csp, "linode")
	}
}

func TestResolveCredentialsPriority(t *testing.T) {
	// Flag takes priority over env
	root := deployCmd
	root.ResetFlags()
	root.Flags().String("credentials", "", "")
	root.Flags().String("credentials-file", "", "")
	root.Flags().Bool("non-interactive", false, "")

	root.Flags().Set("credentials", `{"target":{"csp":"aws","account":"{}"}}`)
	os.Setenv("PYXCLOUD_CREDENTIALS", `{"target":{"csp":"gcp","account":"{}"}}`)
	defer os.Unsetenv("PYXCLOUD_CREDENTIALS")

	payload, err := resolveCredentials(root)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Target.Csp != "aws" {
		t.Errorf("Flag should take priority, got csp=%q", payload.Target.Csp)
	}
}

func TestResolveCredentialsNone(t *testing.T) {
	root := deployCmd
	root.ResetFlags()
	root.Flags().String("credentials", "", "")
	root.Flags().String("credentials-file", "", "")
	root.Flags().Bool("non-interactive", false, "")
	os.Unsetenv("PYXCLOUD_CREDENTIALS")

	payload, err := resolveCredentials(root)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Error("Expected nil payload when no credentials provided")
	}
}

func TestParsePayloadMissingTarget(t *testing.T) {
	_, err := parsePayload(`{"source":{"csp":"aws","account":"{}"}}`)
	if err == nil {
		t.Error("Expected error when target is missing")
	}
}

func TestParsePayloadInvalidJSON(t *testing.T) {
	_, err := parsePayload(`{invalid}`)
	if err == nil {
		t.Error("Expected error on invalid JSON")
	}
}

func TestDetectMigration(t *testing.T) {
	tests := []struct {
		version    string
		isMigration bool
	}{
		{"0.1.0", false},
		{"0.9.5", false},
		{"1.0.0", true},
		{"2.1.3", true},
	}
	for _, tt := range tests {
		got := detectMigration(tt.version)
		if got != tt.isMigration {
			t.Errorf("detectMigration(%q): got %v, want %v", tt.version, got, tt.isMigration)
		}
	}
}

func TestCspFieldsAllProviders(t *testing.T) {
	expectedProviders := []string{
		"aws", "azure", "gcp", "digitalocean", "linode",
		"ubicloud", "vultr", "oracle", "ibm", "alibaba",
		"stackit", "ovh",
	}
	for _, p := range expectedProviders {
		if _, ok := cspFields[p]; !ok {
			t.Errorf("Missing credential fields for provider %q", p)
		}
	}
}

func TestInlineDeployPayloadSerialization(t *testing.T) {
	payload := &inlineDeployPayload{
		Target: &credentialBlock{
			Csp:     "aws",
			Account: `{"access_key_id":"AKIA","secret_access_key":"secret"}`,
		},
		Source: &credentialBlock{
			Csp:     "gcp",
			Account: `{"project_id":"proj-123"}`,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var parsed inlineDeployPayload
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.Target.Csp != "aws" {
		t.Errorf("Target.Csp: got %q", parsed.Target.Csp)
	}
	if parsed.Source.Csp != "gcp" {
		t.Errorf("Source.Csp: got %q", parsed.Source.Csp)
	}
}

func TestGetSortedProviders(t *testing.T) {
	providers := getSortedProviders()
	if len(providers) != 12 {
		t.Errorf("Expected 12 providers, got %d", len(providers))
	}
	if providers[0] != "aws" {
		t.Errorf("First provider should be 'aws', got %q", providers[0])
	}
}
