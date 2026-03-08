package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// cspFields maps each cloud provider to its required credential fields.
var cspFields = map[string][]credentialField{
	"aws": {
		{Key: "access_key_id", Label: "AWS Access Key ID", Secret: true},
		{Key: "secret_access_key", Label: "AWS Secret Access Key", Secret: true},
	},
	"azure": {
		{Key: "subscription_id", Label: "Azure Subscription ID", Secret: false},
		{Key: "client_id", Label: "Azure Client (App) ID", Secret: false},
		{Key: "client_secret", Label: "Azure Client Secret", Secret: true},
		{Key: "tenant_id", Label: "Azure Tenant ID", Secret: false},
	},
	"gcp": {
		{Key: "credentials_json", Label: "Path to GCP Service Account JSON file", Secret: false, IsFile: true},
	},
	"digitalocean": {
		{Key: "token", Label: "DigitalOcean API Token", Secret: true},
	},
	"linode": {
		{Key: "token", Label: "Linode API Token", Secret: true},
	},
	"ubicloud": {
		{Key: "email", Label: "Ubicloud Email", Secret: false},
		{Key: "apiKey", Label: "Ubicloud API Key", Secret: true},
	},
	"vultr": {
		{Key: "apiKey", Label: "Vultr API Key", Secret: true},
	},
	"oracle": {
		{Key: "tenancy_ocid", Label: "Oracle Tenancy OCID", Secret: false},
		{Key: "user_ocid", Label: "Oracle User OCID", Secret: false},
		{Key: "fingerprint", Label: "Oracle API Key Fingerprint", Secret: false},
		{Key: "private_key", Label: "Path to Oracle private key PEM file", Secret: false, IsFile: true},
	},
	"ibm": {
		{Key: "api_key", Label: "IBM Cloud API Key", Secret: true},
	},
	"alibaba": {
		{Key: "access_key_id", Label: "Alibaba Access Key ID", Secret: true},
		{Key: "access_key_secret", Label: "Alibaba Access Key Secret", Secret: true},
	},
	"stackit": {
		{Key: "token", Label: "STACKIT Service Account Token", Secret: true},
		{Key: "project_id", Label: "STACKIT Project ID", Secret: false},
	},
	"ovh": {
		{Key: "application_key", Label: "OVH Application Key", Secret: true},
		{Key: "application_secret", Label: "OVH Application Secret", Secret: true},
		{Key: "consumer_key", Label: "OVH Consumer Key", Secret: true},
		{Key: "endpoint", Label: "OVH Endpoint (e.g. ovh-eu)", Secret: false},
	},
}

// credentialField describes a single credential field for interactive prompts.
type credentialField struct {
	Key    string
	Label  string
	Secret bool   // mask input
	IsFile bool   // read file contents instead of raw value
}

// credentialBlock is the JSON payload sent to the backend.
type credentialBlock struct {
	Csp     string `json:"csp"`
	Account string `json:"account"`
}

// inlineDeployPayload is the full request body for /cli/deploy/{id}/{ver}/inline.
type inlineDeployPayload struct {
	Source *credentialBlock `json:"source,omitempty"`
	Target *credentialBlock `json:"target"`
}

// resolveCredentials reads credentials from flags, env, or file.
// Priority: --credentials flag > --credentials-file flag > PYXCLOUD_CREDENTIALS env.
func resolveCredentials(cmd *cobra.Command) (*inlineDeployPayload, error) {
	// 1. Direct JSON flag
	credsJSON, _ := cmd.Flags().GetString("credentials")
	if credsJSON != "" {
		return parsePayload(credsJSON)
	}

	// 2. JSON file flag
	credsFile, _ := cmd.Flags().GetString("credentials-file")
	if credsFile != "" {
		data, err := os.ReadFile(credsFile)
		if err != nil {
			return nil, fmt.Errorf("read credentials file: %w", err)
		}
		return parsePayload(string(data))
	}

	// 3. Environment variable
	envCreds := os.Getenv("PYXCLOUD_CREDENTIALS")
	if envCreds != "" {
		return parsePayload(envCreds)
	}

	return nil, nil // no credentials provided
}

func parsePayload(raw string) (*inlineDeployPayload, error) {
	var p inlineDeployPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("parse credentials JSON: %w", err)
	}
	if p.Target == nil {
		return nil, fmt.Errorf("credentials JSON must contain a 'target' block")
	}
	return &p, nil
}

// promptCredentials interactively prompts for target (and optionally source) credentials.
func promptCredentials(isMigration bool) (*inlineDeployPayload, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nInteractive Credential Setup")
	fmt.Println(strings.Repeat("─", 40))

	// Target
	fmt.Println("\n📦 Target Provider (where to deploy):")
	targetBlock, err := promptProviderCredentials(reader)
	if err != nil {
		return nil, fmt.Errorf("target credentials: %w", err)
	}

	payload := &inlineDeployPayload{Target: targetBlock}

	// Source (migration only)
	if isMigration {
		fmt.Println("\n📤 Source Provider (where to migrate from):")
		sourceBlock, err := promptProviderCredentials(reader)
		if err != nil {
			return nil, fmt.Errorf("source credentials: %w", err)
		}
		payload.Source = sourceBlock
	}

	return payload, nil
}

func promptProviderCredentials(reader *bufio.Reader) (*credentialBlock, error) {
	// List available providers
	providers := getSortedProviders()
	fmt.Printf("  Available: %s\n", strings.Join(providers, ", "))
	fmt.Print("  Provider: ")
	csp, _ := reader.ReadString('\n')
	csp = strings.TrimSpace(csp)

	fields, ok := cspFields[csp]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", csp)
	}

	account := make(map[string]interface{})
	for _, f := range fields {
		fmt.Printf("  %s: ", f.Label)
		val, _ := reader.ReadString('\n')
		val = strings.TrimSpace(val)
		if val == "" {
			return nil, fmt.Errorf("field %s is required", f.Key)
		}
		if f.IsFile {
			data, err := os.ReadFile(val)
			if err != nil {
				return nil, fmt.Errorf("read file %s: %w", val, err)
			}
			val = string(data)
		}
		account[f.Key] = val
	}

	accountJSON, err := json.Marshal(account)
	if err != nil {
		return nil, fmt.Errorf("marshal account: %w", err)
	}

	return &credentialBlock{Csp: csp, Account: string(accountJSON)}, nil
}

// detectMigration returns true if the build version indicates a migration (>= 1.0).
func detectMigration(buildVersion string) bool {
	return !strings.HasPrefix(buildVersion, "0.")
}

func getSortedProviders() []string {
	return []string{
		"aws", "azure", "gcp", "digitalocean", "linode",
		"ubicloud", "vultr", "oracle", "ibm", "alibaba",
		"stackit", "ovh",
	}
}
