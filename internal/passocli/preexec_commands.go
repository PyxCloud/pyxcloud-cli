package passocli

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"github.com/spf13/cobra"
)

// newPreexecCommands builds the bounded project, connect, discovery, docs, and
// scope command group. Each generated operation is executed through Runtime.Perform
// so mutations retain the normal ledger and evidence behavior.
func newPreexecCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) []*cobra.Command {
	projects := &cobra.Command{Use: "projects", Args: cobra.NoArgs}
	projects.AddCommand(operationCommand("list", "projects", "projects:projectList", false, false, makeRuntime, nil))
	projects.AddCommand(operationCommand("create", "projects", "projects:projectCreate", false, false, makeRuntime, nil))
	projects.AddCommand(operationCommand("get", "projects", "projects:projectRead", true, false, makeRuntime, nil))

	connect := &cobra.Command{Use: "connect", Args: cobra.NoArgs}
	start := &cobra.Command{Use: "start", Args: cobra.NoArgs}
	start.Flags().String("purpose", "install", "connection purpose: install, link, or add_repos")
	start.Flags().String("installation", "", "existing GitHub installation ID")
	start.RunE = func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		purpose, _ := cmd.Flags().GetString("purpose")
		if purpose != "install" && purpose != "link" && purpose != "add_repos" {
			return &ExitError{20, "invalid_input"}
		}
		body := map[string]any{"purpose": purpose, "projectId": r.ProjectID}
		if installation, _ := cmd.Flags().GetString("installation"); installation != "" {
			body["installationId"] = installation
		}
		input, _ := json.Marshal(body)
		return performAndEmit(cmd.Context(), r, "connect", "connect:githubConnectStart", nil, nil, input, false)
	}
	connect.AddCommand(start, newConnectAttachCommand(makeRuntime))

	discover := &cobra.Command{Use: "discover", Args: cobra.NoArgs}
	read := operationCommand("read", "discovery", "define:defineAnalysisRead", true, false, makeRuntime, func(cmd *cobra.Command) (url.Values, error) {
		wait, _ := cmd.Flags().GetInt("wait")
		if wait < 0 || wait > 20 {
			return nil, &ExitError{20, "invalid_input"}
		}
		q := url.Values{}
		if cmd.Flags().Changed("wait") {
			q.Set("wait", strconv.Itoa(wait))
		}
		return q, nil
	})
	read.Flags().Int("wait", 0, "wait seconds (0 to 20)")
	discover.RunE = read.RunE
	discover.Flags().Int("wait", 0, "wait seconds (0 to 20)")
	discover.AddCommand(read,
		operationCommand("start", "discovery", "define:defineAnalysisStart", true, false, makeRuntime, nil),
		operationCommand("approve", "discovery", "define:defineAnalysisApprove", true, false, makeRuntime, nil),
		operationCommand("cancel", "discovery", "define:defineAnalysisCancel", true, false, makeRuntime, nil),
		operationCommand("retry", "discovery", "define:defineAnalysisRetry", true, false, makeRuntime, nil),
		operationCommand("confirm", "discovery", "define:defineAnalysisConfirm", true, false, makeRuntime, nil),
		operationCommand("skip", "discovery", "define:defineAnalysisSkip", true, false, makeRuntime, nil),
		operationCommand("decisions", "discovery", "define:defineAnalysisSaveDecisions", true, false, makeRuntime, nil),
		operationCommand("opinions", "discovery", "define:defineAnalysisSaveOpinions", true, false, makeRuntime, nil),
	)

	docs := &cobra.Command{Use: "docs", Args: cobra.NoArgs}
	docsRead := operationCommand("read", "documentation", "define:documentationRead", true, false, makeRuntime, nil)
	docs.RunE = docsRead.RunE
	docs.AddCommand(docsRead,
		operationCommand("generate", "documentation", "define:documentationGenerate", true, false, makeRuntime, nil),
		operationCommand("compilations", "documentation", "vibe-docs-boardos:listDocumentCompilations", true, false, makeRuntime, nil),
		operationCommand("compile", "documentation", "vibe-docs-boardos:compileDocumentation", true, true, makeRuntime, nil),
	)

	define := &cobra.Command{Use: "define", Args: cobra.NoArgs}
	assessmentRead := operationCommand("assessment-read", "scope", "define:scopeAssessmentRead", true, false, makeRuntime, nil)
	define.RunE = assessmentRead.RunE
	define.AddCommand(assessmentRead,
		operationCommand("assess", "scope", "define:scopeAssessmentStart", true, false, makeRuntime, nil),
		operationCommand("forecast", "scope", "define:scopeForecast", false, false, makeRuntime, nil),
		operationCommand("derive", "scope", "define:scopeDerivation", true, false, makeRuntime, nil),
	)
	return []*cobra.Command{projects, connect, discover, docs, define}
}

// operationInputSupport follows the generated HTTP method and explicit route exceptions.
// GET operations and documented bodyless POSTs never expose --input; optional-body
// POSTs expose it without requiring it, while all other generated body commands require it.
func operationInputSupport(operation string) (hasInput, required bool) {
	op, ok := passocontract.Operations[operation]
	if !ok {
		return false, false
	}
	if strings.EqualFold(op.Method, "GET") {
		return false, false
	}
	switch operation {
	case "define:defineAnalysisStart", "define:scopeDerivation":
		return false, false
	case "define:documentationGenerate", "define:defineAnalysisRetry", "define:defineAnalysisCancel", "define:defineAnalysisSkip":
		return true, false
	default:
		return true, true
	}
}

// operationCommand creates a single catalog-backed command. A path-project
// operation always requires the selected project from the root --project flag.
func operationCommand(use, stage, operation string, projectRequired, bodyIdempotency bool, makeRuntime func(*cobra.Command) (*Runtime, error), query func(*cobra.Command) (url.Values, error)) *cobra.Command {
	cmd := &cobra.Command{Use: use, Args: cobra.NoArgs}
	hasInput, inputRequired := operationInputSupport(operation)
	if hasInput {
		cmd.Flags().String("input", "", "JSON input file or - for stdin")
		if inputRequired {
			_ = cmd.MarkFlagRequired("input")
		}
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		var input json.RawMessage
		var err error
		if hasInput {
			inputPath, _ := cmd.Flags().GetString("input")
			input, err = ReadInput(inputPath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if inputRequired && len(input) == 0 {
				return &ExitError{20, "invalid_input"}
			}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params := map[string]string{}
		if projectRequired {
			if r.ProjectID <= 0 {
				return &ExitError{20, "project_required"}
			}
			paramName := "projectId"
			if strings.HasPrefix(operation, "vibe-docs-boardos:") {
				paramName = "id"
			}
			params[paramName] = strconv.FormatInt(r.ProjectID, 10)
		}
		var q url.Values
		if query != nil {
			q, err = query(cmd)
			if err != nil {
				return err
			}
		}
		return performAndEmit(cmd.Context(), r, stage, operation, params, q, input, bodyIdempotency)
	}
	return cmd
}

func performAndEmit(ctx context.Context, r *Runtime, stage, operation string, params map[string]string, query url.Values, input json.RawMessage, bodyIdempotency bool) error {
	result, err := r.Perform(ctx, stage, operation, params, query, input, bodyIdempotency)
	if err != nil {
		return err
	}
	return r.Emit(result)
}

func newConnectAttachCommand(makeRuntime func(*cobra.Command) (*Runtime, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "attach", Args: cobra.NoArgs}
	var repos []string
	cmd.Flags().StringArrayVar(&repos, "repo", nil, "repository identifier (repeatable)")
	_ = cmd.MarkFlagRequired("repo")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if len(repos) == 0 {
			return &ExitError{20, "invalid_input"}
		}
		clean := make([]string, 0, len(repos))
		seen := map[string]bool{}
		for _, repo := range repos {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				return &ExitError{20, "invalid_input"}
			}
			if !seen[repo] {
				seen[repo] = true
				clean = append(clean, repo)
			}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}
		snapshot, err := readCanonicalProjectState(cmd.Context(), r, params)
		if err != nil {
			return err
		}
		alreadyAttached := true
		for _, repo := range clean {
			if !contains(snapshot.Repos, repo) {
				alreadyAttached = false
				break
			}
		}
		if alreadyAttached {
			return r.Emit(snapshot.Result)
		}
		if snapshot.State == "CONNECT.NEW" {
			if _, err = canonicalTransition(cmd.Context(), r, params, "repo.connect.requested", nil, snapshot.Version); err != nil {
				return err
			}
			snapshot, err = readCanonicalProjectState(cmd.Context(), r, params)
			if err != nil {
				return err
			}
		}
		if snapshot.State != "CONNECT.ADD_REPO" {
			return &ExitError{30, "connect_state_conflict"}
		}
		result, err := canonicalTransition(cmd.Context(), r, params, "repo.connected", map[string]any{"repos": clean}, snapshot.Version)
		if err != nil {
			return err
		}
		return r.Emit(result)
	}
	return cmd
}

type canonicalSnapshot struct {
	Result  Result
	State   string
	Version int64
	Repos   []string
}

func readCanonicalProjectState(ctx context.Context, r *Runtime, params map[string]string) (canonicalSnapshot, error) {
	result, err := r.Perform(ctx, "connect", "projects:canonicalProjectStateRead", params, nil, nil, false)
	if err != nil {
		return canonicalSnapshot{}, err
	}
	var body struct {
		State   string `json:"state"`
		Version int64  `json:"version"`
		Data    struct {
			Repos []string `json:"repos"`
		} `json:"data"`
	}
	if json.Unmarshal(result.Data, &body) != nil || body.State == "" || body.Version < 0 {
		return canonicalSnapshot{}, &ExitError{30, "invalid_project_state"}
	}
	return canonicalSnapshot{Result: result, State: body.State, Version: body.Version, Repos: body.Data.Repos}, nil
}

func canonicalTransition(ctx context.Context, r *Runtime, params map[string]string, event string, payload map[string]any, version int64) (Result, error) {
	body := map[string]any{"event": event, "version": version}
	if payload != nil {
		body["payload"] = payload
	}
	input, err := json.Marshal(body)
	if err != nil {
		return Result{}, &ExitError{20, "invalid_input"}
	}
	return r.Perform(ctx, "connect", "projects:canonicalProjectTransition", params, nil, input, false)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
