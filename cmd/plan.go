package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pyxcloud/pyxcloud-cli/internal/pyxfile"

	"github.com/spf13/cobra"
)

// planCmd — "pyx plan": parse a Pyxfile locally and emit the canonical plan
// JSON on stdout (or --out FILE). No network calls.
var planCmd = &cobra.Command{
	Use:   "plan [file]",
	Short: "Compile a Pyxfile locally into the canonical plan JSON (no deploy)",
	Long: `Parse a Pyxfile and compile it into the canonical plan JSON.

This is the local, offline counterpart of 'pyxcloud pyxfile plan': it parses
and validates the Pyxfile (app/env header, component vocabulary, ARCH block)
and prints the canonical plan JSON to stdout. No API calls are made.

  pyx plan Pyxfile
  pyx plan --out plan.json Pyxfile`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "Pyxfile"
		if len(args) == 1 {
			path = args[0]
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read Pyxfile %q: %w", path, err)
		}
		plan, err := pyxfile.Parse(string(text))
		if err != nil {
			return err // validation errors exit 1 via root Execute
		}
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		out = append(out, '\n')
		outPath, _ := cmd.Flags().GetString("out")
		if outPath == "" {
			fmt.Print(string(out))
			return nil
		}
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			return fmt.Errorf("write plan %q: %w", outPath, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "plan written to %s (%d components)\n",
			outPath, len(plan.Components))
		return nil
	},
	// Keep the error message on one line for CI logs.
	SilenceUsage: true,
}

func init() {
	planCmd.Flags().String("out", "", "Write the plan JSON here instead of stdout")
	rootCmd.AddCommand(planCmd)
}
