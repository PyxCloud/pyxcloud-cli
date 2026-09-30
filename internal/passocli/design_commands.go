package passocli

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newDesignCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) []*cobra.Command {
	inputFlag := func(cmd *cobra.Command) {
		cmd.Flags().String("input", "", "JSON object file, or - for stdin")
	}
	readInput := func(cmd *cobra.Command, required bool) (json.RawMessage, error) {
		path, _ := cmd.Flags().GetString("input")
		if required && path == "" {
			return nil, &ExitError{20, "input_required"}
		}
		return ReadInput(path, cmd.InOrStdin())
	}
	withRuntime := func(cmd *cobra.Command, checkVersion bool, run func(*Runtime) error) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		if checkVersion && strings.TrimSpace(r.VersionID) == "" {
			return &ExitError{20, "version_required"}
		}
		return run(r)
	}
	architecture := &cobra.Command{Use: "design", Short: "Read or change project architecture", Args: cobra.NoArgs}
	architecture.RunE = func(cmd *cobra.Command, _ []string) error {
		return withRuntime(cmd, false, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "architecture", "architecture:getArchitecture", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, url.Values{}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}

	generation := &cobra.Command{Use: "generate", Short: "Request an architecture proposal", Args: cobra.NoArgs}
	inputFlag(generation)
	generation.RunE = func(cmd *cobra.Command, _ []string) error {
		input, err := readInput(cmd, true)
		if err != nil {
			return err
		}
		return withRuntime(cmd, false, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "architecture", "architecture:createArchitectureGeneration", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, url.Values{}, input, true)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}

	generationRead := &cobra.Command{Use: "generation <id>", Short: "Read an architecture generation", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return withRuntime(cmd, false, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "architecture", "architecture:getArchitectureGeneration", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "generationId": args[0]}, url.Values{}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}}
	proposalRead := &cobra.Command{Use: "proposal <id>", Short: "Read an architecture proposal", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return withRuntime(cmd, false, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "architecture", "architecture:getArchitectureProposal", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "proposalId": args[0]}, url.Values{}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}}
	selectArchitecture := &cobra.Command{Use: "choose", Short: "Select an architecture proposal", Args: cobra.NoArgs}
	inputFlag(selectArchitecture)
	selectArchitecture.RunE = func(cmd *cobra.Command, _ []string) error {
		input, err := readInput(cmd, true)
		if err != nil {
			return err
		}
		return withRuntime(cmd, false, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "architecture", "architecture:createArchitectureSelection", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, url.Values{}, input, true)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}
	architecture.AddCommand(generation, generationRead, proposalRead, selectArchitecture)

	compare := &cobra.Command{Use: "compare", Short: "Read or change cloud comparison", Args: cobra.NoArgs}
	compare.RunE = func(cmd *cobra.Command, _ []string) error {
		return withRuntime(cmd, true, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "cloud", "regioncompare.v2:getCloudCompareV2", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "versionId": r.VersionID}, url.Values{}, nil, false)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}
	evaluate := &cobra.Command{Use: "evaluate", Short: "Start cloud evaluation", Args: cobra.NoArgs}
	inputFlag(evaluate)
	evaluate.RunE = func(cmd *cobra.Command, _ []string) error {
		input, err := readInput(cmd, false)
		if err != nil {
			return err
		}
		return withRuntime(cmd, true, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "cloud", "regioncompare.v2:startCloudEvaluation", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "versionId": r.VersionID}, url.Values{}, input, true)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}
	chooseCloud := &cobra.Command{Use: "choose", Short: "Select a cloud provider and region", Args: cobra.NoArgs}
	inputFlag(chooseCloud)
	chooseCloud.RunE = func(cmd *cobra.Command, _ []string) error {
		input, err := readInput(cmd, true)
		if err != nil {
			return err
		}
		return withRuntime(cmd, true, func(r *Runtime) error {
			result, err := r.Perform(cmd.Context(), "cloud", "regioncompare.v2:createCloudSelectionV2", map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "versionId": r.VersionID}, url.Values{}, input, true)
			if err != nil {
				return err
			}
			return r.Emit(result)
		})
	}
	compare.AddCommand(evaluate, chooseCloud)

	return []*cobra.Command{architecture, compare}
}
