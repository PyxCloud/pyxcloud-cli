package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import existing cloud infrastructure into PyxCloud",
	Long: `Import commands let you discover and onboard existing cloud resources
into PyxCloud projects — the CLI equivalent of the Import Wizard in the console.

  pyxcloud import discover --account 42
  pyxcloud import build --account 42 --project 51 --select vm-id-1,vm-id-2
  pyxcloud import build --account 42 --project 51 --all`,
}

var importDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover cloud resources from an account binding",
	Long: `Scans the cloud account and returns a grouped list of all discovered
resources (VMs, networks, databases, etc.).

Example:
  pyxcloud import discover --account 42`,
	RunE: func(cmd *cobra.Command, args []string) error {
		accountID, _ := cmd.Flags().GetString("account")
		if accountID == "" {
			return fmt.Errorf("--account is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		fmt.Printf("Discovering resources for account %s...\n", accountID)
		data, err := client.ImportDiscover(accountID)
		if err != nil {
			return fmt.Errorf("discover: %w", err)
		}

		resources, ok := data["resources"]
		if !ok {
			fmt.Println("No resources discovered.")
			return nil
		}

		return renderDiscoveredResources(resources)
	},
}

func renderDiscoveredResources(resources interface{}) error {
	// resources is an array of groups, each containing items
	groups, ok := resources.([]interface{})
	if !ok {
		fmt.Println("No resources discovered.")
		return nil
	}

	totalCount := 0
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "\nID\tTYPE\tNAME\tREGION\tSTATUS")
	fmt.Fprintln(w, "--\t----\t----\t------\t------")

	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := group["items"].([]interface{})
		if !ok {
			continue
		}
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			totalCount++
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n",
				item["id"],
				item["type"],
				item["name"],
				item["region"],
				item["status"])
		}
	}

	w.Flush()
	fmt.Printf("\n%d resources discovered. Use 'pyxcloud import build' to import.\n", totalCount)
	return nil
}

var importBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Create a Build from imported cloud resources",
	Long: `Imports selected (or all) discovered resources into a PyxCloud project
as a new Build version.

Examples:
  # Import specific resources by ID
  pyxcloud import build --account 42 --project 51 --select vm-abc123,vm-def456

  # Import all discovered resources
  pyxcloud import build --account 42 --project 51 --all`,
	RunE: func(cmd *cobra.Command, args []string) error {
		accountID, _ := cmd.Flags().GetString("account")
		projectID, _ := cmd.Flags().GetString("project")
		selectCSV, _ := cmd.Flags().GetString("select")
		importAll, _ := cmd.Flags().GetBool("all")

		if accountID == "" || projectID == "" {
			return fmt.Errorf("--account and --project are required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		var selectedIDs []string

		if importAll {
			// Discover first, then collect all IDs
			fmt.Printf("Discovering all resources for account %s...\n", accountID)
			data, err := client.ImportDiscover(accountID)
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}
			selectedIDs = collectAllResourceIDs(data)
			if len(selectedIDs) == 0 {
				return fmt.Errorf("no resources found to import")
			}
			fmt.Printf("Found %d resources. Importing...\n", len(selectedIDs))
		} else if selectCSV != "" {
			selectedIDs = strings.Split(selectCSV, ",")
			for i := range selectedIDs {
				selectedIDs[i] = strings.TrimSpace(selectedIDs[i])
			}
		} else {
			return fmt.Errorf("either --select or --all is required")
		}

		result, err := client.ImportBuild(projectID, accountID, selectedIDs)
		if err != nil {
			return fmt.Errorf("import build: %w", err)
		}

		fmt.Println("Import build created.")
		fmt.Printf("  Build ID: %v\n", result["buildId"])
		fmt.Printf("  Version:  %v\n", result["version"])
		return nil
	},
}

func collectAllResourceIDs(data map[string]interface{}) []string {
	var ids []string
	resources, ok := data["resources"].([]interface{})
	if !ok {
		return ids
	}
	for _, rawGroup := range resources {
		group, ok := rawGroup.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := group["items"].([]interface{})
		if !ok {
			continue
		}
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := item["id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func init() {
	importDiscoverCmd.Flags().String("account", "", "Account binding ID to scan (required)")
	importBuildCmd.Flags().String("account", "", "Account binding ID (required)")
	importBuildCmd.Flags().StringP("project", "p", "", "Project ID to import into (required)")
	importBuildCmd.Flags().String("select", "", "Comma-separated resource IDs to import")
	importBuildCmd.Flags().Bool("all", false, "Import all discovered resources")

	importCmd.AddCommand(importDiscoverCmd)
	importCmd.AddCommand(importBuildCmd)
	rootCmd.AddCommand(importCmd)
}
