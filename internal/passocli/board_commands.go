package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/spf13/cobra"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var boardOpaque = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
var boardUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type boardCapacity struct {
	Unknown         bool   `json:"unknown,omitempty"`
	RemainingTokens int64  `json:"remainingTokens,omitempty"`
	WindowTokens    int64  `json:"windowTokens,omitempty"`
	ResetAt         string `json:"resetAt,omitempty"`
	ModelClass      string `json:"modelClass,omitempty"`
}
type boardClaim struct {
	AgentID  string         `json:"agentId,omitempty"`
	Capacity *boardCapacity `json:"capacity,omitempty"`
}
type boardUsage struct {
	TokensIn     *int64 `json:"tokensIn,omitempty"`
	TokensOut    *int64 `json:"tokensOut,omitempty"`
	InputTokens  *int64 `json:"inputTokens,omitempty"`
	OutputTokens *int64 `json:"outputTokens,omitempty"`
}
type boardEvidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}
type boardFinish struct {
	FenceToken *int64          `json:"fenceToken"`
	AgentID    string          `json:"agentId,omitempty"`
	Mode       string          `json:"mode,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	ResumeNote string          `json:"resumeNote,omitempty"`
	Usage      *boardUsage     `json:"usage,omitempty"`
	Evidence   []boardEvidence `json:"evidence,omitempty"`
}
type boardVerify struct {
	Mode     string   `json:"mode,omitempty"`
	Verdict  string   `json:"verdict"`
	Findings string   `json:"findings,omitempty"`
	Checks   []string `json:"checks,omitempty"`
}
type boardStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}
type boardPlan struct {
	Steps *[]boardStep `json:"steps"`
	Note  string       `json:"note,omitempty"`
}
type boardExecute struct {
	CommandID string `json:"commandId"`
}

func strictBoardInput(action string, b json.RawMessage) error {
	fail := func() error { return &ExitError{20, "invalid_board_input"} }
	if len(b) == 0 {
		b = json.RawMessage(`{}`)
	}
	var target any
	switch action {
	case "claim":
		target = &boardClaim{}
	case "release", "complete", "resume":
		target = &boardFinish{}
	case "verify":
		target = &boardVerify{}
	case "plan":
		target = &boardPlan{}
	case "execute":
		target = &boardExecute{}
	default:
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		return fail()
	}
	switch v := target.(type) {
	case *boardExecute:
		if !boardUUID.MatchString(v.CommandID) {
			return fail()
		}
	case *boardVerify:
		if v.Mode != "" {
			var fields map[string]json.RawMessage
			if json.Unmarshal(b, &fields) != nil || v.Mode != "independent" || len(fields) != 1 {
				return fail()
			}
			break
		}
		if !strings.EqualFold(v.Verdict, "pass") && !strings.EqualFold(v.Verdict, "fail") {
			return fail()
		}
	case *boardPlan:
		if v.Steps == nil {
			return fail()
		}
	case *boardClaim:
		if v.Capacity != nil && (v.Capacity.RemainingTokens < 0 || v.Capacity.WindowTokens < 0) {
			return fail()
		}
	case *boardFinish:
		if action != "resume" && v.FenceToken == nil {
			return fail()
		}
		if v.FenceToken != nil && *v.FenceToken < 0 {
			return fail()
		}
		if action == "complete" && (v.Usage == nil || v.Reason != "" || v.ResumeNote != "") {
			return fail()
		}
		if v.Mode != "" && (action != "complete" || v.Mode != "independent" || v.FenceToken == nil || *v.FenceToken != 0 || v.AgentID != "") {
			return fail()
		}
		if v.Usage != nil {
			for _, n := range []*int64{v.Usage.TokensIn, v.Usage.TokensOut, v.Usage.InputTokens, v.Usage.OutputTokens} {
				if n != nil && *n < 0 {
					return fail()
				}
			}
			if action == "complete" && ((v.Usage.TokensIn == nil && v.Usage.InputTokens == nil) || (v.Usage.TokensOut == nil && v.Usage.OutputTokens == nil)) {
				return fail()
			}
		}
	}
	return nil
}
func boardInputDescription(cmd *cobra.Command, action string) {
	schemas := map[string]string{
		"claim":   `{"type":"object","properties":{"agentId":{"type":"string"},"capacity":{"type":"object","properties":{"remainingTokens":{"type":"integer"},"windowTokens":{"type":"integer"},"resetAt":{"type":"string","format":"date-time"},"modelClass":{"type":"string"},"unknown":{"type":"boolean"}}}},"additionalProperties":false}`,
		"execute": `{"type":"object","required":["commandId"],"properties":{"commandId":{"type":"string","format":"uuid"}},"additionalProperties":false}`,
		"verify":  `{"oneOf":[{"type":"object","required":["mode"],"properties":{"mode":{"const":"independent"}},"additionalProperties":false},{"type":"object","required":["verdict"],"properties":{"verdict":{"enum":["pass","fail"]},"findings":{"type":"string"},"checks":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}]}`,
		"plan":    `{"type":"object","required":["steps"],"properties":{"steps":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"done":{"type":"boolean"}}}},"note":{"type":"string"}},"additionalProperties":false}`,
	}
	examples := map[string]string{"claim": `{}`, "execute": `{"commandId":"11111111-1111-4111-8111-111111111111"}`, "verify": `{"mode":"independent"}`, "plan": `{"steps":[{"id":"s1","title":"Read the actual task evidence","done":false}]}`, "complete": `{"fenceToken":7,"usage":{"tokensIn":100,"tokensOut":20},"evidence":[]}`, "release": `{"fenceToken":7,"reason":"Actual handoff reason"}`, "resume": `{"resumeNote":"Continue the actual task"}`}
	schema := schemas[action]
	if schema == "" {
		required := `["fenceToken"]`
		if action == "resume" {
			required = `[]`
		}
		if action == "complete" {
			required = `["fenceToken","usage"]`
		}
		schema = `{"type":"object","required":` + required + `,"properties":{"fenceToken":{"type":"integer","minimum":0},"agentId":{"type":"string"},"mode":{"enum":["independent"]},"reason":{"type":"string"},"resumeNote":{"type":"string"},"usage":{"type":"object","properties":{"tokensIn":{"type":"integer","minimum":0},"tokensOut":{"type":"integer","minimum":0},"inputTokens":{"type":"integer","minimum":0},"outputTokens":{"type":"integer","minimum":0}}},"evidence":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},"ref":{"type":"string"},"note":{"type":"string"}}}}},"additionalProperties":false}`
	}
	cmd.Annotations = map[string]string{"inputSchema": schema, "inputExample": examples[action], "bodyRequired": strconv.FormatBool(action != "claim" && action != "resume")}
	cmd.Long = "Canonical Board REST request. Values in examples are illustrative: use actual lease, measured usage and evidence. Completion preserves independent server gates.\n" + schema
	cmd.Example = "  passo --profile staging --project <actual-project> board " + action + " <actual-task> --input request.json\n  Request example: " + examples[action]
}
func newBoardCommands(makeRuntime func(*cobra.Command) (*Runtime, error)) *cobra.Command {
	root := &cobra.Command{Use: "board", Short: "Read and execute canonical scoped board tasks"}
	names := []string{"status", "list", "task", "claim", "release", "plan", "execute", "latest", "availability", "verify", "complete", "resume", "execution", "evidence"}
	for _, name := range names {
		action := name
		read := action == "status" || action == "list" || action == "task" || action == "latest" || action == "availability" || action == "execution" || action == "evidence"
		use := action + " <taskId>"
		args := cobra.ExactArgs(1)
		if action == "status" || action == "list" {
			use = action
			args = cobra.NoArgs
		}
		if action == "execution" {
			use = action + " <executionId>"
		}
		if action == "evidence" {
			use = action + " <artifactId>"
		}
		cmd := &cobra.Command{Use: use, Short: "Canonical board " + action, Args: args}
		if !read {
			cmd.Flags().String("input", "", "Exact canonical JSON request file or - for stdin")
			boardInputDescription(cmd, action)
		}
		if action == "execution" {
			cmd.Flags().Bool("wait", false, "Poll this durable execution until terminal, bounded by --timeout")
			cmd.Flags().String("task", "", "Actual task ID required to verify receipt scope")
		}
		cmd.RunE = func(cmd *cobra.Command, ids []string) error {
			r, e := makeRuntime(cmd)
			if e != nil {
				return e
			}
			if r.ProjectID <= 0 {
				return &ExitError{20, "project_required"}
			}
			params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}
			if len(ids) > 0 {
				if !boardOpaque.MatchString(ids[0]) {
					return &ExitError{20, "invalid_board_identity"}
				}
				key := "taskId"
				if action == "execution" {
					key = "executionId"
				}
				if action == "evidence" {
					key = "artifactId"
				}
				if key != "taskId" && !boardUUID.MatchString(ids[0]) {
					return &ExitError{20, "invalid_board_identity"}
				}
				params[key] = ids[0]
			}
			var input json.RawMessage
			if !read {
				p, _ := cmd.Flags().GetString("input")
				if p == "" && action != "claim" && action != "resume" {
					return &ExitError{20, "input_required"}
				}
				input, e = ReadInput(p, cmd.InOrStdin())
				if e != nil {
					return e
				}
				if e = strictBoardInput(action, input); e != nil {
					return e
				}
			}
			if action == "execution" {
				task, _ := cmd.Flags().GetString("task")
				if !boardOpaque.MatchString(task) {
					return &ExitError{20, "task_required"}
				}
				wait, _ := cmd.Flags().GetBool("wait")
				return r.boardExecutionRead(cmd.Context(), params, task, wait)
			}
			result, e := r.Perform(cmd.Context(), "board", "board-rest:"+action, params, nil, input, false)
			if e != nil {
				return e
			}
			if e = r.Emit(result); e != nil {
				return e
			}
			if result.Status == "blocked" {
				return &ExitError{20, result.Code}
			}
			return nil
		}
		root.AddCommand(cmd)
	}
	return root
}
func (r *Runtime) boardExecutionRead(ctx context.Context, params map[string]string, task string, wait bool) error {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	poll := r.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	for {
		result, e := r.Perform(ctx, "board", "board-rest:execution", params, nil, nil, false)
		if e != nil {
			if ctx.Err() != nil {
				return &ExitError{30, "execution_wait_timeout"}
			}
			return e
		}
		var receipt struct{ ID, TaskID, State string }
		if json.Unmarshal(result.Data, &receipt) != nil || receipt.ID != params["executionId"] || receipt.TaskID != task {
			return &ExitError{30, "execution_scope_mismatch"}
		}
		if e = r.Emit(result); e != nil {
			return e
		}
		switch receipt.State {
		case "succeeded":
			return nil
		case "failed", "cancelled", "canceled", "timed_out":
			return &ExitError{20, "execution_" + receipt.State}
		case "queued", "running":
			if !wait {
				return nil
			}
		default:
			return &ExitError{30, "unknown_execution_state"}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return &ExitError{30, "execution_wait_timeout"}
		case <-timer.C:
		}
	}
}

// Verify any returned task identity before allowing mutation metadata to become
// completed. Console execution receipts additionally bind caller command UUID.
func validateBoardResponse(operation string, params map[string]string, input map[string]json.RawMessage, body json.RawMessage) error {
	var response struct {
		ID        string `json:"id"`
		TaskID    string `json:"taskId"`
		CommandID string `json:"commandId"`
		Task      *struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if json.Unmarshal(body, &response) != nil {
		return &ExitError{30, "invalid_board_response"}
	}
	task := params["taskId"]
	if task != "" && ((response.TaskID != "" && response.TaskID != task) || (response.Task != nil && response.Task.ID != "" && response.Task.ID != task)) {
		return &ExitError{30, "board_task_scope_mismatch"}
	}
	if operation == "board-rest:execute" {
		var command string
		_ = json.Unmarshal(input["commandId"], &command)
		if !boardUUID.MatchString(response.ID) || response.TaskID != task || response.CommandID != command {
			return &ExitError{30, "execution_scope_mismatch"}
		}
	}
	return nil
}
