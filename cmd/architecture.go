package cmd

import (
	"github.com/spf13/cobra"
)

// architectureCmd is the parent command mirroring the "Architecture" sidebar page.
var architectureCmd = &cobra.Command{
	Use:     "architecture",
	Aliases: []string{"arch"},
	Short:   "Manage cloud architectures (builds, compare, deploy, status, destroy)",
	Long: `Architecture commands mirror the Architecture page in the PyxCloud console.
Use these sub-commands to manage your cloud infrastructure lifecycle:

  pyxcloud architecture builds   -p 42
  pyxcloud architecture compare  -p 42 -v 0.1.0
  pyxcloud architecture deploy   -p 42 -v 0.1.0
  pyxcloud architecture status   -p 42 -v 0.1.0
  pyxcloud architecture destroy  -p 42`,
}

func init() {
	rootCmd.AddCommand(architectureCmd)
}
