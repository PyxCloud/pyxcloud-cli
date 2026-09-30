package passocli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

type Options struct {
	Out, Err   io.Writer
	Store      passoauth.Store
	HTTPClient *http.Client
	Now        func() time.Time
}
type Runtime struct {
	Profile                                  passoauth.Profile
	Client                                   *passotransport.Client
	Ledger                                   passostate.Ledger
	LedgerPath, EvidenceDir                  string
	ProjectID                                int64
	VersionID, ReleaseID, RunID, Environment string
	VersionSequence                          int64
	ExpectedVersion                          int64
	JSON                                     bool
	Out, Err                                 io.Writer
	PollInterval                             time.Duration
	timeout                                  time.Duration
	store                                    passoauth.Store
	now                                      func() time.Time
	httpClient                               *http.Client
}
type Result struct {
	SchemaVersion   int             `json:"schemaVersion"`
	Profile         string          `json:"profile,omitempty"`
	Stage           string          `json:"stage,omitempty"`
	Status          string          `json:"status,omitempty"`
	Code            string          `json:"code,omitempty"`
	ProjectID       int64           `json:"projectId,omitempty"`
	VersionID       string          `json:"versionId,omitempty"`
	VersionSequence int64           `json:"versionSequence,omitempty"`
	ReleaseID       string          `json:"releaseId,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	NextAction      any             `json:"nextAction,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	Evidence        []string        `json:"evidence,omitempty"`
}
type ExitError struct {
	ExitCode int
	Code     string
}

func (e *ExitError) Error() string { return e.Code }
func ExitCode(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.ExitCode
	}
	return 20
}
func New(opts Options) *cobra.Command {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Store == nil {
		opts.Store = passoauth.NewKeychainStore()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	var profile, version, release, runID, environment, ledgerPath, evidenceDir string
	var project, expected, versionSequence int64
	var asJSON bool
	var timeout, poll time.Duration
	root := &cobra.Command{Use: "passo", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return &ExitError{20, "command_required"} }}
	root.SetOut(opts.Out)
	root.SetErr(opts.Err)
	f := root.PersistentFlags()
	f.StringVar(&profile, "profile", "sandbox", "API profile")
	f.Int64Var(&project, "project", 0, "project ID")
	f.StringVar(&version, "version", "", "version ID")
	f.Int64Var(&versionSequence, "version-sequence", 0, "numeric project version sequence")
	f.StringVar(&release, "release", "", "release ID")
	f.StringVar(&runID, "run", "", "run ID")
	f.StringVar(&environment, "environment", "staging", "deployment environment")
	f.Int64Var(&expected, "expected-version", 0, "expected version")
	f.StringVar(&ledgerPath, "ledger", ".passo/run.json", "workflow ledger")
	f.StringVar(&evidenceDir, "evidence-dir", ".passo/evidence", "evidence directory")
	f.BoolVar(&asJSON, "json", false, "emit JSON")
	f.DurationVar(&timeout, "timeout", 2*time.Minute, "command timeout")
	f.DurationVar(&poll, "poll-interval", time.Second, "poll interval")
	runtimeFor := func(cmd *cobra.Command) (*Runtime, error) {
		if timeout <= 0 || poll <= 0 {
			return nil, &ExitError{20, "invalid_duration"}
		}
		if environment != "staging" && environment != "production" {
			return nil, &ExitError{20, "invalid_environment"}
		}
		return buildRuntime(cmd, Options{Out: opts.Out, Err: opts.Err, Store: opts.Store, HTTPClient: opts.HTTPClient, Now: opts.Now}, profile, project, version, versionSequence, release, runID, environment, expected, ledgerPath, evidenceDir, asJSON, poll, timeout)
	}
	root.AddCommand(newAuthCommands(runtimeFor)...)
	root.AddCommand(newDesignCommands(runtimeFor)...)
	root.AddCommand(newPreexecCommands(runtimeFor)...)
	root.AddCommand(newReleaseCommands(runtimeFor)...)
	root.AddCommand(newDeployCommands(runtimeFor)...)
	root.AddCommand(newRunCommand(runtimeFor))
	root.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, e := runtimeFor(cmd)
		if e != nil {
			return e
		}
		return r.status(cmd.Context())
	}})
	monitor := &cobra.Command{Use: "monitor", Long: "Read status once by default. With --watch, stream successive status records; with --json these are newline-delimited JSON, followed by a final error record if watching ends due to timeout or cancellation.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, e := runtimeFor(cmd)
		if e != nil {
			return e
		}
		watch, _ := cmd.Flags().GetBool("watch")
		return r.monitor(cmd.Context(), watch)
	}}
	monitor.Flags().Bool("watch", false, "repeat reads; with --json stream NDJSON until timeout or cancellation")
	root.AddCommand(monitor)
	root.AddCommand(newCommandsCatalogCommand(root, &asJSON))
	root.AddCommand(newAgentSetupCommand(os.UserHomeDir))
	return root
}
func buildRuntime(_ *cobra.Command, opts Options, profile string, project int64, version string, versionSequence int64, release, runID, environment string, expected int64, ledgerPath, evidenceDir string, asJSON bool, poll, timeout time.Duration) (*Runtime, error) {
	if versionSequence < 0 {
		return nil, &ExitError{20, "invalid_version_sequence"}
	}
	p, err := passoauth.ResolveProfile(profile)
	if err != nil {
		return nil, &ExitError{20, "invalid_profile"}
	}
	ledger, err := passostate.Load(ledgerPath)
	if err != nil {
		return nil, &ExitError{20, "invalid_ledger"}
	}
	if ledger.Profile != "" && ledger.Profile != profile {
		return nil, &ExitError{20, "scope_mismatch"}
	}
	if ledger.ProjectID > 0 && project > 0 && ledger.ProjectID != project {
		return nil, &ExitError{20, "scope_mismatch"}
	}
	if project == 0 {
		project = ledger.ProjectID
	}
	if project < 0 {
		return nil, &ExitError{20, "invalid_project"}
	}
	if (version != "" && ledger.VersionID != "" && version != ledger.VersionID) || (versionSequence > 0 && ledger.VersionSequence > 0 && versionSequence != ledger.VersionSequence) || (release != "" && ledger.ReleaseID != "" && release != ledger.ReleaseID) || (runID != "" && ledger.RunID != "" && runID != ledger.RunID) {
		return nil, &ExitError{20, "scope_mismatch"}
	}
	if version == "" {
		version = ledger.VersionID
	}
	if versionSequence == 0 {
		versionSequence = ledger.VersionSequence
	}
	if release == "" {
		release = ledger.ReleaseID
	}
	if runID == "" {
		runID = ledger.RunID
	}
	now := opts.Now
	store := opts.Store
	access := func(ctx context.Context) (string, error) {
		if env := os.Getenv("PASSO_ACCESS_TOKEN"); env != "" {
			return env, nil
		}
		tok, e := store.Load(profile)
		if e != nil {
			return "", e
		}
		if tok.ExpiresAt.After(now().Add(30 * time.Second)) {
			return tok.AccessToken, nil
		}
		if tok.RefreshToken == "" {
			return "", errors.New("credentials unavailable")
		}
		fresh, e := (&passoauth.OAuth{Profile: p, HTTPClient: opts.HTTPClient}).Refresh(ctx, tok.RefreshToken)
		if e != nil {
			return "", e
		}
		if e = store.Save(profile, fresh); e != nil {
			return "", e
		}
		return fresh.AccessToken, nil
	}
	client := passotransport.New(p.APIURL, access)
	if opts.HTTPClient != nil {
		client.HTTPClient = opts.HTTPClient
	}
	return &Runtime{Profile: p, Client: client, Ledger: ledger, LedgerPath: ledgerPath, EvidenceDir: evidenceDir, ProjectID: project, VersionID: version, VersionSequence: versionSequence, ReleaseID: release, RunID: runID, Environment: environment, ExpectedVersion: expected, JSON: asJSON, Out: opts.Out, Err: opts.Err, PollInterval: poll, timeout: timeout, store: store, now: now, httpClient: opts.HTTPClient}, nil
}
func (r *Runtime) Emit(v Result) error {
	v.SchemaVersion = 1
	v.Profile = r.Profile.Name
	v.ProjectID = r.ProjectID
	if v.VersionID == "" {
		v.VersionID = r.VersionID
	}
	if v.VersionSequence == 0 {
		v.VersionSequence = r.VersionSequence
	}
	if v.ReleaseID == "" {
		v.ReleaseID = r.ReleaseID
	}
	if v.RunID == "" {
		v.RunID = r.RunID
	}
	if r.JSON {
		return json.NewEncoder(r.Out).Encode(v)
	}
	lines := []string{}
	if v.Status != "" {
		lines = append(lines, "Status: "+v.Status)
	}
	if v.Stage != "" {
		lines = append(lines, "Stage: "+v.Stage)
	}
	if v.Code != "" {
		lines = append(lines, "Code: "+v.Code)
	}
	if action := humanPrimaryAction(v.NextAction); action != nil {
		lines = append(lines, "Next action: "+action.Key)
		if action.Label != "" {
			lines = append(lines, "Label: "+action.Label)
		}
		if action.Href != "" {
			lines = append(lines, "URL: "+action.Href)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "OK")
	}
	if len(lines) > 8 {
		lines = lines[:8]
	}
	_, e := fmt.Fprintln(r.Out, strings.Join(lines, "\n"))
	return e
}
func (r *Runtime) status(ctx context.Context) error {
	if r.ProjectID <= 0 {
		return &ExitError{20, "project_required"}
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	resp, err := r.Client.Do(ctx, http.MethodGet, fmt.Sprintf("/vibe/projects/%d/journey", r.ProjectID), nil, "")
	if err != nil {
		return classify(err)
	}
	var envelope struct {
		Data *struct {
			ProjectID     int64           `json:"projectId"`
			Stage         string          `json:"stage"`
			PrimaryAction json.RawMessage `json:"primaryAction"`
		} `json:"data"`
	}
	if json.Unmarshal(resp.Body, &envelope) != nil || envelope.Data == nil || envelope.Data.ProjectID <= 0 || envelope.Data.ProjectID != r.ProjectID || !validJourneyStage(envelope.Data.Stage) || !validPrimaryAction(envelope.Data.PrimaryAction) {
		return &ExitError{30, "invalid_journey_response"}
	}
	return r.Emit(Result{Stage: envelope.Data.Stage, Status: "observed", NextAction: json.RawMessage(envelope.Data.PrimaryAction), Data: resp.Body})
}
func validJourneyStage(stage string) bool {
	switch stage {
	case "pre_execution", "board", "gate", "architecture", "cloud", "deploy", "live":
		return true
	}
	return false
}
func validPrimaryAction(raw json.RawMessage) bool {
	var action map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &action) != nil || action == nil {
		return false
	}
	var key string
	if json.Unmarshal(action["key"], &key) != nil {
		return false
	}
	switch key {
	case "continue_pre_execution", "open_board", "freeze", "open_security_report", "fix_on_board", "design_architecture", "choose_cloud", "deploy", "follow_deploy", "retry_deploy", "open_app":
		return true
	default:
		return false
	}
}

type humanAction struct{ Key, Label, Href string }

func humanPrimaryAction(raw any) *humanAction {
	var data []byte
	switch v := raw.(type) {
	case json.RawMessage:
		data = v
	case []byte:
		data = v
	default:
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	var action humanAction
	if json.Unmarshal(fields["key"], &action.Key) != nil || !validPrimaryAction(data) {
		return nil
	}
	_ = json.Unmarshal(fields["label"], &action.Label)
	_ = json.Unmarshal(fields["href"], &action.Href)
	action.Key = cleanHumanField(action.Key)
	action.Label = cleanHumanField(action.Label)
	action.Href = cleanHumanField(action.Href)
	return &action
}

func cleanHumanField(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 256 {
		value = string(runes[:256])
	}
	return value
}
func (r *Runtime) monitor(ctx context.Context, watch bool) error {
	if !watch {
		return r.status(ctx)
	}
	deadline, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	for {
		if err := deadline.Err(); err != nil {
			return monitorContextError(err)
		}
		if err := r.status(deadline); err != nil {
			if contextErr := deadline.Err(); contextErr != nil {
				return monitorContextError(contextErr)
			}
			return err
		}
		timer := time.NewTimer(r.PollInterval)
		select {
		case <-deadline.Done():
			timer.Stop()
			return monitorContextError(deadline.Err())
		case <-timer.C:
		}
	}
}
func monitorContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &ExitError{20, "canceled"}
	}
	return &ExitError{20, "deadline_exceeded"}
}
func classify(err error) error {
	var api *passotransport.APIError
	if errors.As(err, &api) {
		c := api.Code
		if c == "not_human" || c == "step_up_required" {
			return &ExitError{10, c}
		}
		if api.StatusCode == 503 || strings.HasSuffix(c, "_unwired") || strings.HasSuffix(c, "_unavailable") {
			return &ExitError{30, c}
		}
		return &ExitError{20, c}
	}
	if errors.Is(err, context.Canceled) {
		return &ExitError{20, "canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ExitError{20, "timeout"}
	}
	return &ExitError{20, "request_failed"}
}
