package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var accountsCmd = &cobra.Command{
	Use:     "accounts",
	Aliases: []string{"acc"},
	Short:   "Manage cloud account bindings",
	Long: `Account commands mirror the Account Binding page in the PyxCloud console.
Use these sub-commands to manage your cloud provider connections:

  pyxcloud accounts list
  pyxcloud accounts create --provider aws --credentials '...'
  pyxcloud accounts delete --id 42
  pyxcloud accounts verify --id 42`,
}

var accountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all account bindings",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		accounts, err := client.AccountList()
		if err != nil {
			return fmt.Errorf("list accounts: %w", err)
		}

		if len(accounts) == 0 {
			fmt.Println("No account bindings found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNICKNAME\tPROVIDER\tSTATUS")
		fmt.Fprintln(w, "--\t--------\t--------\t------")
		for _, a := range accounts {
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
				a["id"], a["nickname"], a["provider"], a["status"])
		}
		return w.Flush()
	},
}

var accountsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new account binding",
	Long: `Creates a new cloud provider account binding.

Examples:
  # With inline credentials JSON
  pyxcloud accounts create --provider aws --credentials '{"access_key_id":"...","secret_access_key":"..."}'

  # With credentials from a file
  pyxcloud accounts create --provider gcp --credentials-file sa-key.json

  # With a custom nickname
  pyxcloud accounts create --provider azure --nickname "prod-azure" --credentials '...'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		nickname, _ := cmd.Flags().GetString("nickname")
		credsJSON, _ := cmd.Flags().GetString("credentials")
		credsFile, _ := cmd.Flags().GetString("credentials-file")

		if provider == "" {
			return fmt.Errorf("--provider is required")
		}

		// Resolve credentials
		creds := credsJSON
		if creds == "" && credsFile != "" {
			data, err := os.ReadFile(credsFile)
			if err != nil {
				return fmt.Errorf("read credentials file: %w", err)
			}
			creds = string(data)
		}
		if creds == "" {
			return fmt.Errorf("--credentials or --credentials-file is required")
		}

		// Validate it's valid JSON
		if !json.Valid([]byte(creds)) {
			return fmt.Errorf("credentials must be valid JSON")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{
			"provider":    provider,
			"credentials": creds,
			"nickname":    nickname,
		}

		result, err := client.AccountCreate(body)
		if err != nil {
			return fmt.Errorf("create account: %w", err)
		}

		fmt.Println("Account binding created.")
		fmt.Printf("  ID:       %v\n", result["id"])
		fmt.Printf("  Nickname: %v\n", result["nickname"])
		fmt.Printf("  Provider: %v\n", result["provider"])
		return nil
	},
}

var accountsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete an account binding",
	RunE: func(cmd *cobra.Command, args []string) error {
		accountID, _ := cmd.Flags().GetString("id")
		if accountID == "" {
			return fmt.Errorf("--id is required")
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("This will permanently delete account binding %s.\n", accountID)
			fmt.Print("Type 'yes' to confirm: ")
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		if err := client.AccountDelete(accountID); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}

		fmt.Printf("Account binding %s deleted.\n", accountID)
		return nil
	},
}

var accountsVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify account binding credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		accountID, _ := cmd.Flags().GetString("id")
		if accountID == "" {
			return fmt.Errorf("--id is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		result, err := client.AccountVerify(accountID)
		if err != nil {
			return fmt.Errorf("verify account: %w", err)
		}

		valid, _ := result["valid"].(bool)
		if valid {
			fmt.Printf("Account %s: credentials are valid ✓\n", accountID)
		} else {
			errMsg, _ := result["error"].(string)
			fmt.Printf("Account %s: credentials are invalid ✗\n", accountID)
			if errMsg != "" {
				fmt.Printf("  Error: %s\n", errMsg)
			}
		}
		return nil
	},
}

func init() {
	accountsCreateCmd.Flags().String("provider", "", "Cloud provider (aws, azure, gcp, digitalocean, etc.) (required)")
	accountsCreateCmd.Flags().String("nickname", "", "Account nickname (optional)")
	accountsCreateCmd.Flags().String("credentials", "", "Credentials JSON string")
	accountsCreateCmd.Flags().String("credentials-file", "", "Path to credentials JSON file")
	accountsDeleteCmd.Flags().String("id", "", "Account binding ID to delete (required)")
	accountsDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	accountsVerifyCmd.Flags().String("id", "", "Account binding ID to verify (required)")

	accountsCmd.AddCommand(accountsListCmd)
	accountsCmd.AddCommand(accountsCreateCmd)
	accountsCmd.AddCommand(accountsDeleteCmd)
	accountsCmd.AddCommand(accountsVerifyCmd)
	rootCmd.AddCommand(accountsCmd)
}
