package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy all infrastructure for a project",
	Long: `Triggers infrastructure destruction for all deployed resources
in the specified project. This action is irreversible.

Example:
  pyxcloud architecture destroy -p 42
  pyxcloud architecture destroy -p 42 --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			return fmt.Errorf("--project is required")
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("This will destroy ALL infrastructure for project %s.\n", projectID)
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

		fmt.Printf("Destroying infrastructure for project %s...\n", projectID)

		result, err := client.Destroy(projectID)
		if err != nil {
			return fmt.Errorf("destroy failed: %w", err)
		}

		fmt.Printf("%v\n", result["message"])
		logv("Status: %v", result["status"])
		return nil
	},
}

func init() {
	destroyCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	destroyCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	architectureCmd.AddCommand(destroyCmd)
}
