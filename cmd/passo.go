package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pyxcloud/pyxcloud-cli/internal/api"
	"github.com/pyxcloud/pyxcloud-cli/internal/config"
	"github.com/spf13/cobra"
)

// passo status reads the journey read endpoint (backend
// go/internal/journeyread: GET /vibe/projects/:id/journey) and reports the
// journey stage plus the next action. Exit codes: 0 ok, 10 blocked,
// 20 not authenticated, 30 other error.

// The types below mirror the backend journey contract so the JSON shape is
// the contract's, not an invented one:
//   - apicontract.Envelope[T] (platform/pyx-backend go/internal/apicontract/envelope.go)
//   - journeyreadcontract.JourneyRead (go/internal/journeyreadcontract/dto.go)
//   - journeyreadcontract.PrimaryAction (dto.go)
//
// Facts is passed through verbatim (json.RawMessage) — the CLI never
// re-shapes server-owned fields.
type journeyEnvelope struct {
	Data journeyRead `json:"data"`
}

type journeyRead struct {
	ProjectId     int64           `json:"projectId"`
	Version       *versionRef     `json:"version"`
	Stage         string          `json:"stage"`
	SubState      string          `json:"subState,omitempty"`
	PrimaryAction primaryAction   `json:"primaryAction"`
	NeedsYou      []needsYouItem  `json:"needsYou"`
	Facts         json.RawMessage `json:"facts"`
	Projection    projection      `json:"projection"`
}

// primaryAction mirrors journeyreadcontract.PrimaryAction.
type primaryAction struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Href    string `json:"href"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type needsYouItem struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Href    string `json:"href"`
}

// versionRef mirrors journeyreadcontract.VersionRef.
type versionRef struct {
	Id       string `json:"id"`
	Label    string `json:"label"`
	Sequence int    `json:"sequence"`
	LockedAt string `json:"lockedAt,omitempty"`
}

type projection struct {
	Macro string `json:"macro"`
	Micro string `json:"micro,omitempty"`
}

// passoStatusOutput is the --json payload: the journey plus its next action.
type passoStatusOutput struct {
	Journey    journeyRead   `json:"journey"`
	NextAction primaryAction `json:"nextAction"`
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
	data, statusCode, err := client.DoRequest(http.MethodGet, "/vibe/projects/"+projectID+"/journey", nil)
	if err != nil {
		return &exitError{code: ExitError, msg: fmt.Sprintf("journey request: %v", err)}
	}
	if statusCode == http.StatusUnauthorized {
		return &exitError{code: ExitNotAuthenticated, msg: "not authenticated (HTTP 401): re-run pyxcloud auth login"}
	}
	if statusCode != http.StatusOK {
		return &exitError{code: ExitError, msg: fmt.Sprintf("journey endpoint returned HTTP %d: %s", statusCode, string(data))}
	}

	var env journeyEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return &exitError{code: ExitError, msg: fmt.Sprintf("decode journey response: %v", err)}
	}
	journey := env.Data
	if journey.Stage == "" {
		return &exitError{code: ExitError, msg: "journey response missing stage (contract violation)"}
	}

	out := passoStatusOutput{Journey: journey, NextAction: journey.PrimaryAction}
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return &exitError{code: ExitError, msg: fmt.Sprintf("encode status: %v", err)}
		}
	} else {
		fmt.Printf("Project: %d\n", journey.ProjectId)
		fmt.Printf("Stage: %s", journey.Stage)
		if journey.SubState != "" {
			fmt.Printf(" (%s)", journey.SubState)
		}
		fmt.Println()
		if journey.Version != nil {
			fmt.Printf("Version: %s (v%d)\n", journey.Version.Label, journey.Version.Sequence)
		}
		fmt.Printf("Next action: %s — %s", journey.PrimaryAction.Key, journey.PrimaryAction.Label)
		if !journey.PrimaryAction.Allowed && journey.PrimaryAction.Reason != "" {
			fmt.Printf(" (blocked: %s)", journey.PrimaryAction.Reason)
		}
		fmt.Println()
		for _, n := range journey.NeedsYou {
			fmt.Printf("Needs you: [%s] %s (%s)\n", n.Kind, n.Message, n.Href)
		}
	}

	if !journey.PrimaryAction.Allowed || len(journey.NeedsYou) > 0 {
		return &exitError{code: ExitBlocked, msg: "blocked: the next action is not allowed or needs-you items are pending"}
	}
	return nil
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
