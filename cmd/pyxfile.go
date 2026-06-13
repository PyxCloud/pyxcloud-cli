package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// pyxfileCmd is the parent command for the declarative Pyxfile IaC format.
var pyxfileCmd = &cobra.Command{
	Use:   "pyxfile",
	Short: "Compile, deploy and export Pyxfiles (declarative IaC)",
	Long: `Work with Pyxfiles — PyxCloud's declarative Infrastructure-as-Code format.

A Pyxfile compiles to the canonical architecture topology and resolved
provider/region, then deploys through the same pipeline as the console.

  pyxcloud pyxfile plan   -f Pyxfile
  pyxcloud pyxfile apply  -f Pyxfile --account-binding 42 --approval-token <token>
  pyxcloud pyxfile export -f topology.json
  pyxcloud pyxfile status`,
}

// printPyxJSON pretty-prints a JSON response body (local helper; avoids depending
// on render helpers that may live in unmerged CLI work).
func printPyxJSON(body []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		fmt.Println(string(body))
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}

func readPyxfile(cmd *cobra.Command) ([]byte, error) {
	path, _ := cmd.Flags().GetString("file")
	if path == "" {
		path = "Pyxfile"
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Pyxfile %q: %w", path, err)
	}
	return text, nil
}

// pyxfilePlanCmd — "terraform plan" equivalent: compile without deploying.
var pyxfilePlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Compile a Pyxfile and show the resulting topology (no deploy)",
	RunE: func(cmd *cobra.Command, args []string) error {
		text, err := readPyxfile(cmd)
		if err != nil {
			return err
		}
		client, err := getClient()
		if err != nil {
			return err
		}
		// The plan endpoint consumes the raw Pyxfile as text/plain.
		body, status, err := client.DoRaw(
			"POST", "/vibe/architecture/pyxfile/plan", text, "text/plain", nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("pyxfile plan failed (HTTP %d): %s", status, string(body))
		}
		return printPyxJSON(body)
	},
}

// pyxfileApplyCmd — "terraform apply" equivalent: compile and deploy.
var pyxfileApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Compile a Pyxfile and deploy it",
	RunE: func(cmd *cobra.Command, args []string) error {
		text, err := readPyxfile(cmd)
		if err != nil {
			return err
		}
		accountBinding, _ := cmd.Flags().GetInt64("account-binding")
		approvalToken, _ := cmd.Flags().GetString("approval-token")

		payload := map[string]interface{}{"pyxfile": string(text)}
		if accountBinding > 0 {
			payload["accountBindingId"] = accountBinding
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		headers := map[string]string{}
		if approvalToken != "" {
			headers["X-Approval-Token"] = approvalToken
		}
		client, err := getClient()
		if err != nil {
			return err
		}
		body, status, err := client.DoRaw(
			"POST", "/vibe/deploy/pyxfile", data, "application/json", headers)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("pyxfile apply failed (HTTP %d): %s", status, string(body))
		}
		return printPyxJSON(body)
	},
}

// pyxfileExportCmd — convert a topology JSON into a Pyxfile.
var pyxfileExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export an architecture topology (JSON) as a Pyxfile",
	Long: `Convert a canonical architecture topology JSON into a human-editable Pyxfile.

  pyxcloud pyxfile export -f topology.json
  pyxcloud pyxfile export -f topology.json --app shop -o Pyxfile`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("file")
		if path == "" {
			return fmt.Errorf("--file (topology JSON) is required")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read topology %q: %w", path, err)
		}
		var topology interface{}
		if err := json.Unmarshal(raw, &topology); err != nil {
			return fmt.Errorf("topology is not valid JSON: %w", err)
		}
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		payload := map[string]interface{}{"topology": topology}
		if app != "" {
			payload["app"] = app
		}
		if env != "" {
			payload["env"] = env
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		client, err := getClient()
		if err != nil {
			return err
		}
		body, status, err := client.DoRaw(
			"POST", "/vibe/architecture/pyxfile/export", data, "application/json", nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("pyxfile export failed (HTTP %d): %s", status, string(body))
		}
		var resp struct {
			Pyxfile string `json:"pyxfile"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("unexpected export response: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		if output != "" {
			if err := os.WriteFile(output, []byte(resp.Pyxfile), 0644); err != nil {
				return fmt.Errorf("write %q: %w", output, err)
			}
			fmt.Printf("Pyxfile written to %s\n", output)
			return nil
		}
		fmt.Print(resp.Pyxfile)
		return nil
	},
}

// pyxfileStatusCmd — list tracked auto-migration deployments.
var pyxfileStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tracked Pyxfile auto-migration deployments",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}
		body, status, err := client.DoRaw(
			"GET", "/vibe/deploy/pyxfile/deployments", nil, "", nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("pyxfile status failed (HTTP %d): %s", status, string(body))
		}
		return printPyxJSON(body)
	},
}

func init() {
	pyxfilePlanCmd.Flags().StringP("file", "f", "Pyxfile", "Path to the Pyxfile")
	pyxfileApplyCmd.Flags().StringP("file", "f", "Pyxfile", "Path to the Pyxfile")
	pyxfileApplyCmd.Flags().Int64("account-binding", 0, "AccountBinding id to deploy with")
	pyxfileApplyCmd.Flags().String("approval-token", "", "Host-issued approval token (X-Approval-Token)")
	pyxfileExportCmd.Flags().StringP("file", "f", "", "Path to the topology JSON to export")
	pyxfileExportCmd.Flags().StringP("output", "o", "", "Write the Pyxfile here instead of stdout")
	pyxfileExportCmd.Flags().String("app", "", "APP name to embed in the Pyxfile")
	pyxfileExportCmd.Flags().String("env", "", "ENV tag to embed in the Pyxfile")

	pyxfileCmd.AddCommand(pyxfilePlanCmd)
	pyxfileCmd.AddCommand(pyxfileApplyCmd)
	pyxfileCmd.AddCommand(pyxfileExportCmd)
	pyxfileCmd.AddCommand(pyxfileStatusCmd)
	rootCmd.AddCommand(pyxfileCmd)
}
