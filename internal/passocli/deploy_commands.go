package passocli

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newDeployCommands provides read and managed deployment operations through
// their generated contracts. Seal authorization remains a browser action.
func newDeployCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) []*cobra.Command {
	readInput := func(cmd *cobra.Command) (json.RawMessage, error) {
		path, _ := cmd.Flags().GetString("input")
		if path == "" {
			return nil, &ExitError{20, "input_required"}
		}
		return ReadInput(path, cmd.InOrStdin())
	}
	inputFlag := func(cmd *cobra.Command) {
		cmd.Flags().String("input", "", "JSON object file or - for stdin")
		_ = cmd.MarkFlagRequired("input")
	}
	project := func(r *Runtime) (map[string]string, error) {
		if r.ProjectID <= 0 {
			return nil, &ExitError{20, "project_required"}
		}
		return map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}, nil
	}
	performRead := func(cmd *cobra.Command, r *Runtime, stage, op string, params map[string]string, q url.Values) (Result, error) {
		res, err := r.Perform(cmd.Context(), stage, op, params, q, nil, false)
		return res, err
	}

	seal := &cobra.Command{Use: "seal", Short: "Inspect deploy preconditions; complete authorization in the browser", Args: cobra.NoArgs}
	sealRead := func(cmd *cobra.Command, r *Runtime) error {
		params, err := project(r)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r.ReleaseID) == "" {
			return &ExitError{20, "release_required"}
		}
		params["releaseId"] = r.ReleaseID
		res, err := performRead(cmd, r, "seal", "seal:deployPreconditionsRead", params, url.Values{"environment": []string{r.Environment}})
		if err != nil {
			return err
		}
		res.Status = "human_required"
		res.Code = "browser_authorization_required"
		res.NextAction = map[string]string{"key": "authorize_deploy", "href": strings.TrimRight(r.Profile.ConsoleURL, "/") + "/projects/" + strconv.FormatInt(r.ProjectID, 10) + "/deploy?release=" + url.QueryEscape(r.ReleaseID)}
		if err = r.Emit(res); err != nil {
			return err
		}
		return &ExitError{10, "browser_authorization_required"}
	}
	seal.RunE = func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		return sealRead(cmd, r)
	}
	seal.AddCommand(&cobra.Command{Use: "authorize", Short: "Open browser authorization guidance (never authorizes from CLI)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		return sealRead(cmd, r)
	}})

	deploy := &cobra.Command{Use: "deploy", Short: "Read, create, and manage deployments", Args: cobra.NoArgs}
	deployRead := &cobra.Command{Use: "read", Short: "Read the selected managed deployment run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := project(r)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r.RunID) == "" {
			return &ExitError{20, "run_required"}
		}
		params["runId"] = r.RunID
		res, err := performRead(cmd, r, "deploy", "journeycontract:managedDeploymentRead", params, nil)
		if err != nil {
			return err
		}
		return r.Emit(res)
	}}
	deploy.RunE = deployRead.RunE
	deploy.AddCommand(deployRead)
	list := &cobra.Command{Use: "list", Short: "List deployments", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := project(r)
		if err != nil {
			return err
		}
		res, err := performRead(cmd, r, "deploy", "deployment:listDeployments", params, nil)
		if err != nil {
			return err
		}
		return r.Emit(res)
	}}
	deploy.AddCommand(list)
	create := &cobra.Command{Use: "create", Short: "Create a managed deployment", Args: cobra.NoArgs}
	inputFlag(create)
	create.Flags().Bool("wait", false, "poll the created run until it reaches a terminal state or timeout")
	create.RunE = func(cmd *cobra.Command, _ []string) error {
		input, err := readInput(cmd)
		if err != nil {
			return err
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := project(r)
		if err != nil {
			return err
		}
		res, err := r.Perform(cmd.Context(), "deploy", "journeycontract:managedDeploymentCreate", params, nil, input, true)
		if err != nil {
			return err
		}
		wait, _ := cmd.Flags().GetBool("wait")
		if wait {
			if r.RunID == "" {
				return &ExitError{30, "invalid_api_response"}
			}
			observed, terminalErr := waitManagedRun(cmd.Context(), r)
			if terminalErr != nil {
				return terminalErr
			}
			res = observed
		}
		return r.Emit(res)
	}
	deploy.AddCommand(create)

	for _, item := range []struct{ name, op string }{{"cancel", "deployment:cancelDeployment"}, {"retry", "deployment:retryDeployment"}, {"promote", "deployment:promoteDeployment"}, {"rollback", "deployment:rollbackDeployment"}} {
		item := item
		cmd := &cobra.Command{Use: item.name + " <deployment-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(args[0]) == "" {
				return &ExitError{20, "invalid_deployment_id"}
			}
			r, err := makeRuntime(cmd)
			if err != nil {
				return err
			}
			res, err := r.Perform(cmd.Context(), "deploy", item.op, map[string]string{"deploymentId": args[0]}, nil, input, true)
			if err != nil {
				return err
			}
			return r.Emit(res)
		}}
		inputFlag(cmd)
		deploy.AddCommand(cmd)
	}
	destroy := &cobra.Command{Use: "destroy", Short: "Terminate the selected managed deployment", Args: cobra.NoArgs}
	inputFlag(destroy)
	destroy.Flags().Bool("sweep", false, "unavailable: backend does not expose a zero-resource sweeper")
	destroy.RunE = func(cmd *cobra.Command, _ []string) error {
		sweep, _ := cmd.Flags().GetBool("sweep")
		if sweep {
			return &ExitError{30, "sweep_unavailable"}
		}
		input, err := readInput(cmd)
		if err != nil {
			return err
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(input, &body) != nil || body == nil {
			return &ExitError{20, "invalid_input"}
		}
		var confirmation string
		_ = json.Unmarshal(body["confirmation"], &confirmation)
		if confirmation != "TERMINATE" {
			return &ExitError{20, "termination_confirmation_required"}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		params, err := project(r)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r.RunID) == "" {
			return &ExitError{20, "run_required"}
		}
		params["runId"] = r.RunID
		res, err := r.Perform(cmd.Context(), "deploy", "journeycontract:managedDeploymentTerminate", params, nil, input, true)
		if err != nil {
			return err
		}
		return r.Emit(res)
	}
	deploy.AddCommand(destroy)
	return []*cobra.Command{seal, deploy}
}

// waitManagedRun observes the backend DTO's closed lifecycle states. It reports
// the latest observed record and never synthesizes a completed state.
func waitManagedRun(ctx context.Context, r *Runtime) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10), "runId": r.RunID}
	for {
		res, err := r.Perform(ctx, "deploy", "journeycontract:managedDeploymentRead", params, nil, nil, false)
		if err != nil {
			if ctx.Err() == context.Canceled {
				return Result{}, &ExitError{20, "canceled"}
			}
			if ctx.Err() == context.DeadlineExceeded {
				return Result{}, &ExitError{20, "deadline_exceeded"}
			}
			return Result{}, err
		}
		state, ok := managedRunState(res.Data)
		if !ok {
			return Result{}, &ExitError{30, "invalid_deployment_response"}
		}
		if state == "succeeded" || state == "failed" || state == "cancelled" {
			res.Status = "observed"
			if state != "succeeded" {
				return res, &ExitError{20, "deployment_" + state}
			}
			return res, nil
		}
		t := time.NewTimer(r.PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			if ctx.Err() == context.Canceled {
				return Result{}, &ExitError{20, "canceled"}
			}
			return Result{}, &ExitError{20, "deadline_exceeded"}
		case <-t.C:
		}
	}
}

func managedRunState(raw json.RawMessage) (string, bool) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "", false
	}
	var value struct {
		State string `json:"state"`
	}
	data := envelope.Data
	if len(data) == 0 {
		data = raw
	}
	if json.Unmarshal(data, &value) != nil || value.State == "" {
		return "", false
	}
	switch value.State {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return value.State, true
	}
	return "", false
}
