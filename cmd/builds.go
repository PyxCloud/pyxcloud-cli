package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "List builds for a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			return fmt.Errorf("--project is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		builds, err := client.Builds(projectID)
		if err != nil {
			return fmt.Errorf("list builds: %w", err)
		}

		if len(builds) == 0 {
			fmt.Println("No builds found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "VERSION\tSTATUS\tDEPLOY VERSION")
		fmt.Fprintln(w, "-------\t------\t--------------")
		for _, b := range builds {
			fmt.Fprintf(w, "%v\t%v\t%v\n",
				b["version"], b["status"], b["deployVersion"])
		}
		return w.Flush()
	},
}

func init() {
	buildsCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	architectureCmd.AddCommand(buildsCmd)
}
