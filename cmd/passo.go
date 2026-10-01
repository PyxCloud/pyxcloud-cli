package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/pyxcloud/pyxcloud-cli/internal/api"
	"github.com/pyxcloud/pyxcloud-cli/internal/api/gen/journey"
	"github.com/pyxcloud/pyxcloud-cli/internal/config"
	"github.com/spf13/cobra"
)

// passo status reads the journey read endpoint (backend
// go/internal/journeyread: GET /vibe/projects/:id/journey) through the client
// generated from the vendored contract api/contracts/journey.openapi.json
// (package internal/api/gen/journey, operationId getJourney) and reports the
// journey stage plus the next action. Exit codes: 0 ok, 10 blocked,
// 20 not authenticated, 30 other error.
//
// The typed JourneyEnvelope drives the CLI's own decisions (stage present,
// next action allowed, needs-you pending). The --json payload is cut from the
// raw response body instead, so server-owned fields (facts, and anything a
// newer backend adds) are passed through verbatim and never re-shaped by the
// generated types.

// passoStatusOutput is the --json payload: the journey plus its next action,
// both verbatim from the response envelope's data.
type passoStatusOutput struct {
	Journey    json.RawMessage `json:"journey"`
	NextAction json.RawMessage `json:"nextAction"`
}

var passoCmd = &cobra.Command{
	Use:   "passo",
	Short: "Journey status (passo): where the project is and what to do next",
}

var passoStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Journey stage + next action for a project",
	RunE:  runPassoStatus,
}

func runPassoStatus(cmd *cobra.Command, args []string) error {
	projectID, _ := cmd.Flags().GetString("project")
	asJSON, _ := cmd.Flags().GetBool("json")
	if projectID == "" {
		return &exitError{code: ExitError, msg: "--project is required"}
	}
	pid, err := strconv.ParseInt(projectID, 10, 64)
	if err != nil {
		return &exitError{code: ExitError, msg: fmt.Sprintf("--project must be a numeric project id, got %q", projectID)}
	}

	cfg, err := config.LoadProfile(profile)
	if err != nil {
		return &exitError{code: ExitError, msg: err.Error()}
	}
	if cfg.Token == "" && cfg.RefreshToken == "" {
		msg := "Not authenticated. Run: pyxcloud auth login"
		if profile != "" {
			msg = fmt.Sprintf("Not authenticated for profile %q. Run: pyxcloud --profile %s auth login", profile, profile)
		}
		return &exitError{code: ExitNotAuthenticated, msg: msg}
	}
	if apiURL != "" {
		cfg.APIURL = apiURL
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://beta-api.pyxcloud.io"
	}

	client := api.NewClientFromConfig(cfg)
	jc, err := journey.NewClientWithResponses(cfg.APIURL,
		journey.WithHTTPClient(client.HTTPClient),
		journey.WithRequestEditorFn(client.Authorize))
	if err != nil {
		return &exitError{code: ExitError, msg: fmt.Sprintf("journey client: %v", err)}
	}
	resp, err := jc.GetJourneyWithResponse(cmd.Context(), pid)
	if err != nil {
		return &exitError{code: ExitError, msg: fmt.Sprintf("journey request: %v", err)}
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		return &exitError{code: ExitNotAuthenticated, msg: "not authenticated (HTTP 401): re-run pyxcloud auth login"}
	}
	if resp.StatusCode() != http.StatusOK {
		return &exitError{code: ExitError, msg: fmt.Sprintf("journey endpoint returned HTTP %d: %s", resp.StatusCode(), string(resp.Body))}
	}
	if resp.JSON200 == nil || resp.JSON200.Data == nil {
		return &exitError{code: ExitError, msg: "journey response missing data (contract violation)"}
	}
	jr := resp.JSON200.Data
	if jr.Stage == nil || *jr.Stage == "" {
		return &exitError{code: ExitError, msg: "journey response missing stage (contract violation)"}
	}
	var pa journey.PrimaryAction
	if jr.PrimaryAction != nil {
		pa = *jr.PrimaryAction
	}
	var needsYou []journey.NeedsYouItem
	if jr.NeedsYou != nil {
		needsYou = *jr.NeedsYou
	}

	if asJSON {
		out, err := rawStatusOutput(resp.Body)
		if err != nil {
			return &exitError{code: ExitError, msg: fmt.Sprintf("encode status: %v", err)}
		}
		if _, err := cmd.OutOrStdout().Write(out); err != nil {
			return &exitError{code: ExitError, msg: fmt.Sprintf("write status: %v", err)}
		}
	} else {
		fmt.Printf("Project: %d\n", deref(jr.ProjectId))
		fmt.Printf("Stage: %s", *jr.Stage)
		if jr.SubState != nil && *jr.SubState != "" {
			fmt.Printf(" (%s)", *jr.SubState)
		}
		fmt.Println()
		if jr.Version != nil {
			fmt.Printf("Version: %s (v%d)\n", deref(jr.Version.Label), deref(jr.Version.Sequence))
		}
		fmt.Printf("Next action: %s — %s", deref(pa.Key), deref(pa.Label))
		if !deref(pa.Allowed) && deref(pa.Reason) != "" {
			fmt.Printf(" (blocked: %s)", *pa.Reason)
		}
		fmt.Println()
		for _, n := range needsYou {
			fmt.Printf("Needs you: [%s] %s (%s)\n", deref(n.Kind), deref(n.Message), deref(n.Href))
		}
	}

	if !deref(pa.Allowed) || len(needsYou) > 0 {
		return &exitError{code: ExitBlocked, msg: "blocked: the next action is not allowed or needs-you items are pending"}
	}
	return nil
}

// rawStatusOutput builds the indented --json payload from the raw journey
// envelope, keeping data and data.primaryAction byte-for-byte as served.
func rawStatusOutput(body []byte) ([]byte, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	var data struct {
		PrimaryAction json.RawMessage `json:"primaryAction"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, err
	}
	next := data.PrimaryAction
	if len(next) == 0 {
		next = json.RawMessage("null")
	}
	compact, err := json.Marshal(passoStatusOutput{Journey: env.Data, NextAction: next})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// deref returns the pointed-to value, or the zero value for nil. The contract
// marks no field required, so the generated types are all pointers.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func init() {
	passoStatusCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	passoStatusCmd.Flags().Bool("json", false, "Print the journey + nextAction JSON payload")
	passoCmd.AddCommand(passoStatusCmd)
	rootCmd.AddCommand(passoCmd)
	// Silence the usage dump and error echo for structured exit codes.
	passoStatusCmd.SilenceUsage = true
	passoStatusCmd.SilenceErrors = true
}
