package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Trigger deployment for a build",
	Long: `Triggers the PyxCloud deployment pipeline for the specified build.

By default, deploy runs in INTERACTIVE mode — prompting for provider
credentials. Use --non-interactive with --credentials or --credentials-file
to skip prompts (e.g. in CI/CD pipelines).

Examples:
  # Interactive (default — prompts for provider and credentials):
  pyxcloud architecture deploy -p 42 -v 0.1.0

  # Non-interactive (CI/CD):
  pyxcloud architecture deploy -p 42 -v 0.1.0 --non-interactive \
    --credentials '{"target":{"csp":"aws","account":"{\"access_key_id\":\"...\",\"secret_access_key\":\"...\"}"}}'

  pyxcloud architecture deploy -p 42 -v 0.1.0 --non-interactive \
    --credentials-file creds.json

  PYXCLOUD_CREDENTIALS='...' pyxcloud architecture deploy -p 42 -v 0.1.0 --non-interactive

  # Standard (uses pre-configured account bindings from the UI):
  pyxcloud architecture deploy -p 42 -v 0.1.0 --non-interactive`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		buildVersion, _ := cmd.Flags().GetString("version")
		isLocal, _ := cmd.Flags().GetBool("local")

		if projectID == "" || buildVersion == "" {
			return fmt.Errorf("--project and --version are required")
		}

		if isLocal {
			return RunLocalDeploy(cmd, projectID, buildVersion)
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		nonInteractive, _ := cmd.Flags().GetBool("non-interactive")

		// Try to resolve inline credentials from flags/file/env
		payload, err := resolveCredentials(cmd)
		if err != nil {
			return fmt.Errorf("credentials: %w", err)
		}

		// Interactive mode (DEFAULT): prompt if no credentials were provided
		if payload == nil && !nonInteractive {
			isMigration := detectMigration(buildVersion)
			payload, err = promptCredentials(isMigration)
			if err != nil {
				return fmt.Errorf("interactive credentials: %w", err)
			}
		}

		// Inline deploy (with credentials — either from prompt or flag)
		if payload != nil {
			fmt.Printf("Deploying project %s build %s (target: %s)...\n",
				projectID, buildVersion, payload.Target.Csp)
			if payload.Source != nil {
				logv("Source: %s (migration)", payload.Source.Csp)
			}

			result, err := client.DeployInline(projectID, buildVersion, payload)
			if err != nil {
				return fmt.Errorf("deploy failed: %w", err)
			}

			fmt.Printf("%v\n", result["message"])
			logv("Status: %v", result["status"])
			if tid, ok := result["ephemeralTargetId"]; ok {
				fmt.Printf("  Ephemeral Target Binding: %v\n", tid)
			}
			fmt.Printf("\n  Check progress: pyxcloud architecture status -p %s -v %s\n", projectID, buildVersion)
			return nil
		}

		// Standard deploy (non-interactive, uses pre-configured account bindings)
		fmt.Printf("Deploying project %s build %s (using account bindings)...\n", projectID, buildVersion)

		result, err := client.Deploy(projectID, buildVersion)
		if err != nil {
			return fmt.Errorf("deploy failed: %w", err)
		}

		fmt.Printf("%v\n", result["message"])
		logv("Status: %v", result["status"])
		fmt.Printf("\n  Check progress: pyxcloud architecture status -p %s -v %s\n", projectID, buildVersion)
		return nil
	},
}

func init() {
	deployCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	deployCmd.Flags().StringP("version", "v", "", "Build version (required)")
	deployCmd.Flags().String("credentials", "", "Inline credentials JSON (target/source blocks)")
	deployCmd.Flags().String("credentials-file", "", "Path to credentials JSON file")
	deployCmd.Flags().Bool("non-interactive", false, "Skip interactive prompts (for CI/CD pipelines)")
	deployCmd.Flags().Bool("local", false, "Execute the deployment script locally instead of PyxCloud servers")
	architectureCmd.AddCommand(deployCmd)
}
