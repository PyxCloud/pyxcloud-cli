package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check deployment status",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		buildVersion, _ := cmd.Flags().GetString("version")
		if projectID == "" || buildVersion == "" {
			return fmt.Errorf("--project and --version are required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		result, err := client.Status(projectID, buildVersion)
		if err != nil {
			return fmt.Errorf("status check: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "  Project:\t%v\n", result["project"])
		fmt.Fprintf(w, "  Version:\t%v\n", result["version"])
		fmt.Fprintf(w, "  Status:\t%v\n", result["status"])
		fmt.Fprintf(w, "  Deploy Version:\t%v\n", result["deployVersion"])
		fmt.Fprintln(w, "")
		return w.Flush()
	},
}

func init() {
	statusCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	statusCmd.Flags().StringP("version", "v", "", "Build version (required)")
	architectureCmd.AddCommand(statusCmd)
}
