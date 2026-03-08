package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var apiURL string
var verbose bool

var rootCmd = &cobra.Command{
	Use:   "pyxcloud",
	Short: "PyxCloud CLI — manage cloud deployments from your pipeline",
	Long: `PyxCloud CLI integrates cloud infrastructure management
into your CI/CD pipelines. Commands mirror the PyxCloud console sidebar.

  pyxcloud auth login
  pyxcloud projects
  pyxcloud architecture builds   -p 42
  pyxcloud architecture compare  -p 42 -v 0.1.0
  pyxcloud architecture deploy   -p 42 -v 0.1.0
  pyxcloud architecture status   -p 42 -v 0.1.0
  pyxcloud architecture destroy  -p 42`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// logv prints a message only when --verbose is set.
func logv(format string, a ...interface{}) {
	if verbose {
		fmt.Printf(format+"\n", a...)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "PyxCloud API URL (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose output")
}
