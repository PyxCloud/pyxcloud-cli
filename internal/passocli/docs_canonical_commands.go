package passocli

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newCanonicalDocumentationCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) []*cobra.Command {
	projectParams := func(r *Runtime) (map[string]string, error) {
		if r.ProjectID <= 0 {
			return nil, &ExitError{20, "project_required"}
		}
		return map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, nil
	}
	versionParams := func(r *Runtime) (map[string]string, error) {
		params, err := projectParams(r)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(r.VersionID) == "" {
			return nil, &ExitError{20, "version_required"}
		}
		params["projectVersionId"] = r.VersionID
		return params, nil
	}
	readCommand := func(use, operation string, paramsFor func(*Runtime) (map[string]string, error), query func(*cobra.Command) (url.Values, error)) *cobra.Command {
		cmd := &cobra.Command{Use: use, Args: cobra.NoArgs}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			r, err := makeRuntime(cmd)
			if err != nil {
				return err
			}
			params, err := paramsFor(r)
			if err != nil {
				return err
			}
			var q url.Values
			if query != nil {
				q, err = query(cmd)
				if err != nil {
					return err
				}
			}
			result, err := r.Perform(cmd.Context(), "docs", operation, params, q, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		}
		return cmd
	}

	workspace := readCommand("workspace", "documentation:documentationWorkspaceRead", projectParams, nil)
	revisions := readCommand("revisions", "documentation:documentationRevisionsList", projectParams, func(cmd *cobra.Command) (url.Values, error) {
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		if (cmd.Flags().Changed("limit") && (limit < 1 || limit > 200)) || offset < 0 {
			return nil, &ExitError{20, "invalid_input"}
		}
		q := url.Values{}
		if cmd.Flags().Changed("limit") {
			q.Set("limit", strconv.Itoa(limit))
		}
		if cmd.Flags().Changed("offset") {
			q.Set("offset", strconv.Itoa(offset))
		}
		return q, nil
	})
	revisions.Flags().Int("limit", 0, "maximum revisions to return (1 to 200)")
	revisions.Flags().Int("offset", 0, "revision list offset (0 or greater)")

	publish := &cobra.Command{Use: "publish", Short: "Publish a named documentation generation as a draft revision", Args: cobra.NoArgs}
	publish.Flags().String("input", "", "JSON object file or - for stdin")
	publish.Flags().Int64("if-match", 0, "optional raw decimal optimistic workspace version")
	_ = publish.MarkFlagRequired("input")
	publish.RunE = func(cmd *cobra.Command, _ []string) error {
		inputPath, _ := cmd.Flags().GetString("input")
		input, err := ReadInput(inputPath, cmd.InOrStdin())
		if err != nil {
			return err
		}
		if len(input) == 0 {
			return &ExitError{20, "input_required"}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := projectParams(r)
		if err != nil {
			return err
		}
		var ifMatch *int64
		if cmd.Flags().Changed("if-match") {
			value, _ := cmd.Flags().GetInt64("if-match")
			ifMatch = &value
		}
		result, err := r.PerformWithIfMatch(cmd.Context(), "docs", "documentation:documentationRevisionPublish", params, nil, input, false, ifMatch)
		if err != nil {
			return err
		}
		return r.Emit(result)
	}

	decision := func(name, operation string) *cobra.Command {
		cmd := &cobra.Command{Use: name + " <revision>", Args: cobra.ExactArgs(1)}
		cmd.Flags().Int64("if-match", 0, "raw decimal optimistic workspace version")
		_ = cmd.MarkFlagRequired("if-match")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("if-match") {
				return &ExitError{20, "if_match_required"}
			}
			r, err := makeRuntime(cmd)
			if err != nil {
				return err
			}
			params, err := projectParams(r)
			if err != nil {
				return err
			}
			params["revisionId"] = args[0]
			ifMatch, _ := cmd.Flags().GetInt64("if-match")
			result, err := r.PerformWithIfMatch(cmd.Context(), "docs", operation, params, nil, nil, false, &ifMatch)
			if err != nil {
				return err
			}
			return r.Emit(result)
		}
		return cmd
	}

	snapshot := &cobra.Command{Use: "snapshot", Args: cobra.NoArgs}
	snapshotRead := readCommand("read", "documentation:documentationSnapshotRead", versionParams, nil)
	lock := &cobra.Command{Use: "lock", Args: cobra.NoArgs}
	lock.Flags().Int64("if-match", 0, "raw decimal optimistic workspace version")
	_ = lock.MarkFlagRequired("if-match")
	lock.RunE = func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("if-match") {
			return &ExitError{20, "if_match_required"}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := versionParams(r)
		if err != nil {
			return err
		}
		ifMatch, _ := cmd.Flags().GetInt64("if-match")
		result, err := r.PerformWithIfMatch(cmd.Context(), "docs", "documentation:documentationSnapshotLock", params, nil, nil, false, &ifMatch)
		if err != nil {
			return err
		}
		return r.Emit(result)
	}
	snapshot.AddCommand(snapshotRead, lock)

	return []*cobra.Command{
		workspace,
		revisions,
		publish,
		decision("begin-review", "documentation:documentationRevisionBeginReview"),
		decision("accept", "documentation:documentationRevisionAccept"),
		decision("reject", "documentation:documentationRevisionReject"),
		snapshot,
	}
}
