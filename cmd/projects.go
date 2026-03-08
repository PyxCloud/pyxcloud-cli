package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		projects, err := client.Projects()
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tDEPLOYED VERSION")
		fmt.Fprintln(w, "--\t----\t----------------")
		for _, p := range projects {
			fmt.Fprintf(w, "%v\t%v\t%v\n",
				p["id"], p["name"], p["deployedVersion"])
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(projectsCmd)
}
