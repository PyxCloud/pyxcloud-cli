package cmd

import (
	"fmt"
	"os"
	"os/exec"
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

var importScanVmsCmd = &cobra.Command{
	Use:   "scan-vms",
	Short: "Agentless SSH deep scan of VM authorized_keys",
	Long: `Connects to the provided IPs using your local SSH command to extract
Public Keys from the authorized_keys files securely, and pushes them to the PyxCloud Web Console.

Example:
  pyxcloud import scan-vms --ips 1.2.3.4,5.6.7.8 --user ubuntu --token abc-123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ipsStr, _ := cmd.Flags().GetString("ips")
		user, _ := cmd.Flags().GetString("user")
		token, _ := cmd.Flags().GetString("token")

		if ipsStr == "" || token == "" {
			return fmt.Errorf("--ips and --token are required")
		}

		ips := strings.Split(ipsStr, ",")
		var foundKeys []string
		keyMap := make(map[string]bool)

		fmt.Printf("Scanning %d Virtual Machine(s) for SSH Keys...\n", len(ips))

		for _, ipRaw := range ips {
			ip := strings.TrimSpace(ipRaw)
			if ip == "" {
				continue
			}
			fmt.Printf(" [~] Scanning %s@%s...\n", user, ip)

			sshCmdStr := fmt.Sprintf("cat ~/.ssh/authorized_keys 2>/dev/null || true")
			out, err := execCmd("ssh", "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes",
				"-o", "ConnectTimeout=5", fmt.Sprintf("%s@%s", user, ip), sshCmdStr)
			if err != nil {
				fmt.Printf(" [!] Warning: Failed to connect or read keys on %s: %v\n", ip, err)
				continue
			}

			lines := strings.Split(string(out), "\n")
			count := 0
			for _, line := range lines {
				l := strings.TrimSpace(line)
				if l != "" && !strings.HasPrefix(l, "#") {
					if !keyMap[l] {
						keyMap[l] = true
						foundKeys = append(foundKeys, l)
						count++
					}
				}
			}
			fmt.Printf(" [+] Found %d unique key(s) on %s\n", count, ip)
		}

		fmt.Printf("Reporting %d unique discovered keys to PyxCloud...\n", len(foundKeys))
		client, err := getClient()
		if err != nil {
			return err
		}

		err = client.DeepScanReport(token, foundKeys)
		if err != nil {
			return fmt.Errorf("failed to report scan results: %w", err)
		}

		fmt.Println("Scan reported successfully! Please continue in the PyxCloud Web UI.")
		return nil
	},
}

// execCmd runs a local OS command returning combined output
func execCmd(name string, arg ...string) ([]byte, error) {
	cmd := exec.Command(name, arg...)
	return cmd.CombinedOutput()
}

func init() {
	importDiscoverCmd.Flags().String("account", "", "Account binding ID to scan (required)")
	importBuildCmd.Flags().String("account", "", "Account binding ID (required)")
	importBuildCmd.Flags().StringP("project", "p", "", "Project ID to import into (required)")
	importBuildCmd.Flags().String("select", "", "Comma-separated resource IDs to import")
	importBuildCmd.Flags().Bool("all", false, "Import all discovered resources")

	importScanVmsCmd.Flags().String("ips", "", "Comma-separated IP addresses of the VMs (required)")
	importScanVmsCmd.Flags().String("user", "ubuntu", "SSH username to connect as")
	importScanVmsCmd.Flags().String("token", "", "Deep scan capability token from the web console (required)")

	importCmd.AddCommand(importDiscoverCmd)
	importCmd.AddCommand(importBuildCmd)
	importCmd.AddCommand(importScanVmsCmd)
	rootCmd.AddCommand(importCmd)
}
