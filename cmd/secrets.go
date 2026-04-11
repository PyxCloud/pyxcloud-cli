package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func getSecretsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pyxcloud")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func getSecretsFile() (string, error) {
	dir, err := getSecretsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.env"), nil
}

func loadSecrets() (map[string]string, error) {
	file, err := getSecretsFile()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return make(map[string]string), nil
	} else if err != nil {
		return nil, err
	}

	secrets := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			secrets[parts[0]] = parts[1] // Value is kept raw
		}
	}
	return secrets, nil
}

func saveSecrets(secrets map[string]string) error {
	file, err := getSecretsFile()
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# PyxCloud Local Deploy Secrets\n")
	sb.WriteString("# This file contains sensitive credentials for local provisioning.\n\n")

	for k, v := range secrets {
		sb.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}

	// Always write with 0600 permissions
	return os.WriteFile(file, []byte(sb.String()), 0600)
}

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage local secrets for local deployment",
	Long: `Manage local secrets used by the "pyxcloud architecture deploy --local" command.
These secrets are saved locally in ~/.pyxcloud/secrets.env and are NOT uploaded to
GitHub Actions. They are used purely for local Terraform execution.`,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set KEY=VALUE",
	Short: "Set a local secret",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		parts := strings.SplitN(args[0], "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid format, use KEY=VALUE")
		}
		key, value := parts[0], parts[1]

		secrets, err := loadSecrets()
		if err != nil {
			return err
		}
		secrets[key] = value

		if err := saveSecrets(secrets); err != nil {
			return err
		}

		fmt.Printf("Secret %s saved.\n", key)
		return nil
	},
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all local secrets (masked)",
	RunE: func(cmd *cobra.Command, args []string) error {
		secrets, err := loadSecrets()
		if err != nil {
			return err
		}

		if len(secrets) == 0 {
			fmt.Println("No local secrets found.")
			return nil
		}

		fmt.Println("Local Secrets (stored in ~/.pyxcloud/secrets.env):")
		for k, v := range secrets {
			masked := "*****"
			if len(v) > 4 {
				masked = v[:2] + "..." + v[len(v)-2:]
			}
			fmt.Printf("  %s = %s\n", k, masked)
		}
		return nil
	},
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete KEY",
	Short: "Delete a local secret",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		secrets, err := loadSecrets()
		if err != nil {
			return err
		}

		if _, exists := secrets[key]; !exists {
			fmt.Printf("Secret %s not found.\n", key)
			return nil
		}

		delete(secrets, key)
		if err := saveSecrets(secrets); err != nil {
			return err
		}

		fmt.Printf("Secret %s deleted.\n", key)
		return nil
	},
}

var secretsImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Interactively import secrets for a specific provider",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		if provider == "" {
			return fmt.Errorf("--provider is required (aws, azure, gcp, ubicloud, digitalocean)")
		}

		fields, ok := cspFields[provider]
		if !ok {
			return fmt.Errorf("unsupported provider: %s", provider)
		}

		// Prompt for each field defined in credentials.go
		fmt.Printf("Please provide credentials for %s.\n", provider)
		creds := map[string]string{}
		for _, f := range fields {
			var value string
			fmt.Printf("%s: ", f.label)
			if f.secret {
				// We don't have terminal.ReadPassword imported globally easily, use plain for now or just standard scan
				// For simplicity in CLI environment
				fmt.Scanln(&value)
			} else {
				fmt.Scanln(&value)
			}
			creds[f.key] = value
		}

		// Map to standard env format used by LocalDeployScriptService
		mapped := map[string]string{}
		switch provider {
		case "aws":
			mapped["AWS_ACCESS_KEY_ID"] = creds["access_key_id"]
			mapped["AWS_SECRET_ACCESS_KEY"] = creds["secret_access_key"]
		case "azure":
			mapped["AZURE_PASSWORD"] = creds["password"]
		case "gcp":
			by, _ := json.Marshal(creds)
			mapped["GCP_CREDENTIALS"] = string(by)
		case "digitalocean":
			mapped["DIGITALOCEAN_TOKEN"] = creds["api_token"]
		case "ubicloud":
			mapped["UBICLOUD_API_TOKEN"] = creds["api_token"]
			// Project ID will be asked / fetched dynamically by tf script or we don't save it here if we don't know it.
			// Actually LocalDeployScriptService ensures UBICLOUD_PROJECT_ID dynamically. Let's just prompt for project_id if it's there
			if pid, ok := creds["project_id"]; ok && pid != "" {
				mapped["UBICLOUD_PROJECT_ID"] = pid
			}
		}

		secrets, err := loadSecrets()
		if err != nil {
			return err
		}
		for k, v := range mapped {
			secrets[k] = v
		}

		if err := saveSecrets(secrets); err != nil {
			return err
		}

		fmt.Printf("Secrets imported for %s.\n", provider)
		return nil
	},
}

func init() {
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	
	secretsImportCmd.Flags().StringP("provider", "p", "", "Cloud provider to import secrets for")
	secretsCmd.AddCommand(secretsImportCmd)

	rootCmd.AddCommand(secretsCmd)
}
