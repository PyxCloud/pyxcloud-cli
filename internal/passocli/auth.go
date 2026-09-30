package passocli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/spf13/cobra"
)

type runtimeFactory func(*cobra.Command) (*Runtime, error)

func newAuthCommands(factory runtimeFactory) []*cobra.Command {
	login := &cobra.Command{Use: "login", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		device, _ := cmd.Flags().GetBool("device")
		r, e := factory(cmd)
		if e != nil {
			return e
		}
		oauth := passoauth.OAuth{Profile: r.Profile, HTTPClient: r.httpClient}
		announce := func(s string) error { _, e := fmt.Fprintln(r.Err, "Open this URL to sign in:", s); return e }
		var token passoauth.Token
		if device {
			a, e := oauth.BeginDevice(cmd.Context())
			if e != nil {
				return &ExitError{20, "login_failed"}
			}
			_, _ = fmt.Fprintf(r.Err, "Visit %s and enter code %s\n", a.VerificationURI, a.UserCode)
			token, e = oauth.CompleteDevice(cmd.Context(), a)
		} else {
			token, e = oauth.LoginPKCE(cmd.Context(), announce)
		}
		if e != nil {
			return classify(e)
		}
		if e = r.store.Save(r.Profile.Name, token); e != nil {
			return &ExitError{20, "credential_save_failed"}
		}
		return r.Emit(Result{Status: "authenticated"})
	}}
	login.Flags().Bool("device", false, "use device authorization")
	logout := &cobra.Command{Use: "logout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, e := factory(cmd)
		if e != nil {
			return e
		}
		if e = r.store.Delete(r.Profile.Name); e != nil {
			return &ExitError{20, "logout_failed"}
		}
		return r.Emit(Result{Status: "logged_out"})
	}}
	return []*cobra.Command{login, logout}
}

// Execute runs the CLI command and emits one machine-readable error when JSON was requested.
func Execute(ctx context.Context, args []string, out, errOut io.Writer) int {
	var stdout, stderr strings.Builder
	cmd := New(Options{Out: &stdout, Err: &stderr})
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	if err == nil {
		_, _ = io.WriteString(out, stdout.String())
		_, _ = io.WriteString(errOut, stderr.String())
		return 0
	}
	code := ExitCode(err)
	msg := err.Error()
	if ee, ok := err.(*ExitError); ok {
		msg = ee.Code
	}
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true
		}
	}
	if jsonMode {
		_ = json.NewEncoder(out).Encode(Result{SchemaVersion: 1, Status: "error", Code: msg})
		return code
	}
	_, _ = fmt.Fprintln(errOut, msg)
	return code
}
