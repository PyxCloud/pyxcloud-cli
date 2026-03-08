package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// keystoreCmd mirrors the "Key Management" sidebar page.
var keystoreCmd = &cobra.Command{
	Use:     "keystore",
	Aliases: []string{"keys"},
	Short:   "Manage SSH keys and key associations",
	Long: `Key Management commands mirror the Key Management page in the PyxCloud console.

  pyxcloud keystore list
  pyxcloud keystore create --label "prod-key"
  pyxcloud keystore delete --id 42`,
}

var keystoreListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all key associations",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		keys, err := client.KeystoreList()
		if err != nil {
			return fmt.Errorf("list keys: %w", err)
		}

		if len(keys) == 0 {
			fmt.Println("No keys found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tLABEL\tSTATUS\tPROJECT\tCREATED")
		fmt.Fprintln(w, "--\t-----\t------\t-------\t-------")
		for _, k := range keys {
			project := "-"
			if p, ok := k["projectName"].(string); ok && p != "" {
				project = p
			}
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n",
				k["id"], k["label"], k["status"], project, k["createdAt"])
		}
		return w.Flush()
	},
}

var keystoreCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new key association",
	RunE: func(cmd *cobra.Command, args []string) error {
		label, _ := cmd.Flags().GetString("label")
		if label == "" {
			return fmt.Errorf("--label is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{"label": label}
		result, err := client.KeystoreCreate(body)
		if err != nil {
			return fmt.Errorf("create key: %w", err)
		}

		fmt.Println("Key created.")
		fmt.Printf("  ID:    %v\n", result["id"])
		fmt.Printf("  Label: %v\n", result["label"])
		if pub, ok := result["publicKey"].(string); ok && pub != "" {
			fmt.Printf("  Public Key: %s\n", pub)
		}
		return nil
	},
}

var keystoreDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a key association",
	RunE: func(cmd *cobra.Command, args []string) error {
		keyID, _ := cmd.Flags().GetString("id")
		if keyID == "" {
			return fmt.Errorf("--id is required")
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("This will permanently delete key %s.\n", keyID)
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

		if err := client.KeystoreDelete(keyID); err != nil {
			return fmt.Errorf("delete key: %w", err)
		}

		fmt.Printf("Key %s deleted.\n", keyID)
		return nil
	},
}

func init() {
	keystoreCreateCmd.Flags().String("label", "", "Key label (required)")
	keystoreDeleteCmd.Flags().String("id", "", "Key ID to delete (required)")
	keystoreDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")

	keystoreCmd.AddCommand(keystoreListCmd)
	keystoreCmd.AddCommand(keystoreCreateCmd)
	keystoreCmd.AddCommand(keystoreDeleteCmd)
	rootCmd.AddCommand(keystoreCmd)
}
