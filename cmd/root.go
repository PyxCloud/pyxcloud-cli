package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/pyxcloud/pyxcloud-cli/internal/config"
	"github.com/spf13/cobra"
)

var apiURL string
var verbose bool
var profile string

// requireProfile validates the --profile flag value before any login stores
// credentials under it.
func requireProfile() error {
	if !config.ValidProfile(profile) {
		return fmt.Errorf("unknown profile %q (supported: sandbox, staging)", profile)
	}
	return nil
}

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
	rootCmd.SilenceUsage = true
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var ec ExitCoder
		if errors.As(err, &ec) {
			os.Exit(ec.ExitCode())
		}
		os.Exit(1)
	}
}

// ExitCoder is an error carrying a process exit code (passo exit-code
// contract: 0 ok, 10 blocked, 20 not authenticated, 30 other error).
type ExitCoder interface {
	error
	ExitCode() int
}

// exitError reports a structured exit code from passo commands.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// Passo exit codes (cli-M0 slice 1).
const (
	ExitOK               = 0
	ExitBlocked          = 10 // journey reached but the next action is not allowed / needs-you items pending
	ExitNotAuthenticated = 20 // no credentials or HTTP 401
	ExitError            = 30 // transport, contract or other error
)

// logv prints a message only when --verbose is set.
func logv(format string, a ...interface{}) {
	if verbose {
		fmt.Printf(format+"\n", a...)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "PyxCloud API URL (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose output")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "Credential profile: sandbox or staging (isolated config file)")
}
