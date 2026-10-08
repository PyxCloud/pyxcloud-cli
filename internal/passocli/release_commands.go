package passocli

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/spf13/cobra"
)

// newReleaseCommands exposes the release freeze and security gate operations
// through their generated API contracts. Bodies are passed through as JSON
// objects so the API remains authoritative about each command's exact shape.
func newReleaseCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) []*cobra.Command {
	inputFlag := func(cmd *cobra.Command) {
		cmd.Flags().String("input", "", "JSON object file, or - for stdin")
	}
	readInput := func(cmd *cobra.Command) (json.RawMessage, error) {
		path, _ := cmd.Flags().GetString("input")
		if path == "" {
			return nil, &ExitError{20, "input_required"}
		}
		return ReadInput(path, cmd.InOrStdin())
	}
	withRuntime := func(cmd *cobra.Command, needsVersion bool, run func(*Runtime) error) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		if needsVersion && strings.TrimSpace(r.VersionID) == "" {
			return &ExitError{20, "version_required"}
		}
		return run(r)
	}
	withVersionLabel := func(cmd *cobra.Command, run func(*Runtime) error) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		if r.VersionLabel == "" {
			return &ExitError{20, "version_label_required"}
		}
		if !passostate.ValidVersionLabel(r.VersionLabel) {
			return &ExitError{20, "invalid_version_label"}
		}
		return run(r)
	}
	withSequenceRuntime := func(cmd *cobra.Command, run func(*Runtime) error) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		if r.VersionSequence <= 0 {
			return &ExitError{20, "version_sequence_required"}
		}
		return run(r)
	}
	performRead := func(cmd *cobra.Command, stage, operation string, version bool, query func(*Runtime) url.Values) error {
		perform := func(r *Runtime) error {
			params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}
			if strings.HasPrefix(operation, "security") {
				params["versionId"] = strconv.FormatInt(r.VersionSequence, 10)
			}
			q := url.Values{}
			if query != nil {
				q = query(r)
			}
			result, err := r.Perform(cmd.Context(), stage, operation, params, q, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		}
		if strings.HasPrefix(operation, "security") {
			return withSequenceRuntime(cmd, perform)
		}
		return withRuntime(cmd, version, perform)
	}
	performMutation := func(cmd *cobra.Command, stage, operation string, needsVersion, bodyKey bool) error {
		input, err := readInput(cmd)
		if err != nil {
			return err
		}
		perform := func(r *Runtime) error {
			params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}
			if needsVersion {
				params["versionId"] = strconv.FormatInt(r.VersionSequence, 10)
			}
			result, err := r.Perform(cmd.Context(), stage, operation, params, url.Values{}, input, bodyKey)
			if err != nil {
				return err
			}
			return r.Emit(result)
		}
		if strings.HasPrefix(operation, "security") {
			return withSequenceRuntime(cmd, perform)
		}
		return withRuntime(cmd, needsVersion, perform)
	}

	freeze := &cobra.Command{Use: "freeze", Short: "Inspect and create release freezes", Args: cobra.NoArgs}
	freeze.AddCommand(&cobra.Command{Use: "preview", Short: "Read the release freeze preview", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return performRead(cmd, "release", "journeycontract:releaseFreezePreviewRead", false, nil)
	}})
	freeze.AddCommand(&cobra.Command{Use: "eligibility", Short: "Read release eligibility", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return performRead(cmd, "release", "journeycontract:releaseEligibilityRead", false, nil)
	}})
	freeze.AddCommand(&cobra.Command{Use: "proposed-pins", Short: "Read proposed release pins", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return performRead(cmd, "release", "journeycontract:releaseProposedPinsRead", false, nil)
	}})
	branches := &cobra.Command{Use: "branches", Short: "Read or materialize release branches", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return withVersionLabel(cmd, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "release", "journeycontract:releaseBranchesPreview", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, url.Values{"version": []string{r.VersionLabel}}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}}
	branchCreate := &cobra.Command{Use: "create", Short: "Materialize release branches", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return withVersionLabel(cmd, func(r *Runtime) error {
			input, err := json.Marshal(map[string]string{"version": r.VersionLabel})
			if err != nil {
				return &ExitError{20, "invalid_input"}
			}
			result, err := r.Perform(cmd.Context(), "release", "journeycontract:releaseBranchesMaterialize", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, url.Values{}, input, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}}
	branches.AddCommand(branchCreate)
	freeze.AddCommand(branches)
	for _, item := range []struct{ use, op, short string }{
		{"create", "journeycontract:releaseFreezeCreate", "Create a release freeze"},
		{"lock", "journeycontract:releaseVersionLockCreate", "Create a release version lock"},
		{"mirror", "journeycontract:releaseSpecRevisionMirror", "Mirror a published spec revision"},
	} {
		item := item
		cmd := &cobra.Command{Use: item.use, Short: item.short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return performMutation(cmd, "release", item.op, false, false)
		}}
		inputFlag(cmd)
		freeze.AddCommand(cmd)
	}

	secure := &cobra.Command{Use: "secure", Short: "Inspect security results and request remediation", Args: cobra.NoArgs}
	for _, item := range []struct{ use, op, short string }{
		{"scan", "securityscan:getSecurityScanRun", "Read the security scan run"},
		{"gate", "securitygate:getSecurityGateEvaluation", "Read the security gate evaluation"},
	} {
		item := item
		secure.AddCommand(&cobra.Command{Use: item.use, Short: item.short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return performRead(cmd, "security", item.op, true, nil)
		}})
	}
	secure.AddCommand(newScanBaselineCommand(makeRuntime))
	secure.AddCommand(&cobra.Command{Use: "finding <id>", Short: "Read security finding details", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return &ExitError{20, "invalid_finding_id"}
		}
		return withSequenceRuntime(cmd, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "security", "securitygate:getSecurityGateFindingDetail", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "versionId": strconv.FormatInt(r.VersionSequence, 10), "findingId": args[0]}, url.Values{}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}})
	for _, item := range []struct{ use, op, short string }{
		{"remediation-preview", "securitygate:requestRemediationPreview", "Request a remediation preview"},
		{"remediation-confirm", "securitygate:confirmRemediationMaterialization", "Confirm human remediation materialization"},
	} {
		item := item
		cmd := &cobra.Command{Use: item.use, Short: item.short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return performMutation(cmd, "security", item.op, true, true)
		}}
		inputFlag(cmd)
		secure.AddCommand(cmd)
	}
	return []*cobra.Command{freeze, secure}
}
