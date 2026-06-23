package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage projects",
	Long: `Project commands for listing, creating, and deleting PyxCloud projects.

  pyxcloud projects list
  pyxcloud projects create --name "production-stack"
  pyxcloud projects delete --id 42`,
	// Default action: list projects (backward-compatible)
	RunE: func(cmd *cobra.Command, args []string) error {
		return projectsListCmd.RunE(cmd, args)
	},
}

var projectsListCmd = &cobra.Command{
	Use:   "list",
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

var projectsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new project",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")

		if name == "" {
			return fmt.Errorf("--name is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{
			"name":        name,
			"description": description,
		}

		result, err := client.ProjectCreate(body)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}

		fmt.Println("Project created.")
		fmt.Printf("  ID:   %v\n", result["id"])
		fmt.Printf("  Name: %v\n", result["name"])
		return nil
	},
}

var projectsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a project and all its builds",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("id")
		if projectID == "" {
			return fmt.Errorf("--id is required")
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("This will permanently delete project %s and all its builds.\n", projectID)
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

		if err := client.ProjectDelete(projectID); err != nil {
			return fmt.Errorf("delete project: %w", err)
		}

		fmt.Printf("Project %s deleted.\n", projectID)
		return nil
	},
}

// projectsStatusCmd projects the board's status view-spec as terminal text — the U8 view-spec
// projector wired to `pyx projects status`. It reads the SAME structured view-spec the web hub
// renders, so the CLI shows a coherent view without parsing HTML. It degrades gracefully (falls
// back to the tool's text/JSON) when the live MCP does not emit a view-spec yet.
var projectsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the board control-room view for a project (view-spec text projection)",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" && len(args) > 0 {
			projectID = args[0]
		}
		if projectID == "" {
			return fmt.Errorf("--project is required (or pass it as the first argument)")
		}
		client, err := boardClient()
		if err != nil {
			return err
		}
		res, err := client.CallTool("passobuild_board_status", map[string]any{
			"projectId":    projectID,
			"editor":       "cli",
			"widgetFormat": "none",
		})
		if err != nil {
			return fmt.Errorf("project status: %w", err)
		}
		renderToolResult(res)
		return nil
	},
}

func init() {
	projectsCreateCmd.Flags().String("name", "", "Project name (required)")
	projectsCreateCmd.Flags().String("description", "", "Project description (optional)")
	projectsDeleteCmd.Flags().String("id", "", "Project ID to delete (required)")
	projectsDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	projectsStatusCmd.Flags().StringP("project", "p", "", "Project ID (required)")

	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsCreateCmd)
	projectsCmd.AddCommand(projectsDeleteCmd)
	projectsCmd.AddCommand(projectsStatusCmd)
	rootCmd.AddCommand(projectsCmd)
}
