package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/spf13/cobra"
)

var runStages = []string{"connect", "discover", "docs", "define", "freeze", "secure", "design", "compare", "seal", "deploy", "monitor"}

type runPlan struct {
	SchemaVersion int       `json:"schemaVersion"`
	Steps         []runStep `json:"steps"`
}
type runStep struct {
	Stage           string              `json:"stage"`
	Operation       string              `json:"operation"`
	Params          map[string]string   `json:"params"`
	Query           map[string][]string `json:"query"`
	Input           json.RawMessage     `json:"input,omitempty"`
	BodyIdempotency bool                `json:"bodyIdempotency"`
	Check           *runCheck           `json:"check"`
}
type runCheck struct {
	Operation string              `json:"operation"`
	Params    map[string]string   `json:"params"`
	Query     map[string][]string `json:"query"`
	Pointer   string              `json:"pointer"`
	Equals    json.RawMessage     `json:"equals"`
}
type runOutput struct {
	SchemaVersion int              `json:"schemaVersion"`
	Status        string           `json:"status"`
	Stage         string           `json:"stage,omitempty"`
	Code          string           `json:"code,omitempty"`
	NextAction    any              `json:"nextAction,omitempty"`
	Results       []runStageResult `json:"results,omitempty"`
}
type runStageResult struct {
	Stage    string `json:"stage"`
	Status   string `json:"status"`
	Observed bool   `json:"observed"`
}

// newRunCommand executes an explicitly authored, bounded plan using generated API operations.
func newRunCommand(makeRuntime func(*cobra.Command) (*Runtime, error)) *cobra.Command {
	var path string
	var target string
	cmd := &cobra.Command{Use: "run", Short: "Run a validated workflow plan through a named stage", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		b, err := readRunPlan(path)
		if err != nil {
			return err
		}
		plan, err := decodeRunPlan(b)
		if err != nil {
			return err
		}
		if err = validateRunPlan(plan); err != nil {
			return err
		}
		ti := stageIndex(target)
		if ti < 0 {
			return &ExitError{20, "invalid_target"}
		}
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.VersionLabel != "" && !passostate.ValidVersionLabel(r.VersionLabel) {
			return &ExitError{20, "invalid_version_label"}
		}
		if planUsesVersionLabel(plan) && r.VersionLabel == "" {
			return &ExitError{20, "version_label_required"}
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), r.timeout)
		defer cancel()
		completed := map[string]bool{}
		for _, s := range plan.Steps {
			if stageIndex(s.Stage) > ti {
				break
			}
			if err := ctx.Err(); err != nil {
				return runContextError(err)
			}
			p := substituteParams(s.Params, r)
			q := substituteValues(s.Query, r)
			if s.Operation == "seal:deployAuthorize" || s.Operation == "securitygate:confirmRemediationMaterialization" {
				return emitRunHandoff(cmd, r, s.Stage, completed)
			}
			if operationStage(s.Operation) != s.Stage {
				return &ExitError{20, "operation_stage_mismatch"}
			}
			if s.Check != nil {
				cp := substituteParams(s.Check.Params, r)
				cq := substituteValues(s.Check.Query, r)
				ok, _, e := checkRun(ctx, r, s.Stage, *s.Check, cp, cq)
				if e != nil {
					return runCallError(ctx, e)
				}
				if ok {
					completed[s.Stage] = true
					continue
				}
			}
			res, e := r.Perform(ctx, s.Stage, s.Operation, p, q, s.Input, s.BodyIdempotency)
			if e != nil {
				return runCallError(ctx, e)
			}
			if s.Check == nil {
				completed[s.Stage] = true
				continue
			}
			for {
				if err := ctx.Err(); err != nil {
					return runContextError(err)
				}
				ok, _, e := checkRun(ctx, r, s.Stage, *s.Check, substituteParams(s.Check.Params, r), substituteValues(s.Check.Query, r))
				if e != nil {
					return runCallError(ctx, e)
				}
				if ok {
					completed[s.Stage] = true
					_ = res
					break
				}
				timer := time.NewTimer(r.PollInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return runContextError(ctx.Err())
				case <-timer.C:
				}
			}
		}
		results := []runStageResult{}
		for _, stage := range runStages {
			if stageIndex(stage) > ti {
				break
			}
			if completed[stage] {
				results = append(results, runStageResult{stage, "observed", true})
			}
		}
		if target == "seal" {
			return emitRunHandoff(cmd, r, "seal", completed)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(runOutput{SchemaVersion: 1, Status: "completed", Stage: target, Results: results})
	}}
	cmd.Flags().StringVar(&target, "to", "", "target stage")
	cmd.Flags().StringVar(&path, "plan", ".passo/walk.json", "workflow plan JSON")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
func emitRunHandoff(cmd *cobra.Command, r *Runtime, stage string, completed map[string]bool) error {
	path := fmt.Sprintf("%s/projects/%d/security", strings.TrimRight(r.Profile.ConsoleURL, "/"), r.ProjectID)
	code := "security_remediation_requires_human"
	if stage == "seal" {
		path = fmt.Sprintf("%s/projects/%d/deploy?release=%s", strings.TrimRight(r.Profile.ConsoleURL, "/"), r.ProjectID, url.QueryEscape(r.ReleaseID))
		code = "browser_authorization_required"
	}
	results := []runStageResult{}
	for _, s := range runStages {
		if completed[s] {
			results = append(results, runStageResult{s, "observed", true})
		}
	}
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(runOutput{SchemaVersion: 1, Status: "human_required", Stage: stage, Code: code, NextAction: map[string]string{"path": path}, Results: results}); err != nil {
		return err
	}
	return &ExitError{10, code}
}

func readRunPlan(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, &ExitError{20, "invalid_plan"}
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if e != nil || len(b) > maxInputBytes {
		return nil, &ExitError{20, "invalid_plan"}
	}
	return b, nil
}
func decodeRunPlan(b []byte) (runPlan, error) {
	var p runPlan
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return p, &ExitError{20, "invalid_plan"}
	}
	var x any
	if e := d.Decode(&x); !errors.Is(e, io.EOF) {
		return p, &ExitError{20, "invalid_plan"}
	}
	return p, nil
}
func validateRunPlan(p runPlan) error {
	if p.SchemaVersion != 1 || len(p.Steps) == 0 {
		return &ExitError{20, "invalid_plan"}
	}
	last := -1
	for _, s := range p.Steps {
		i := stageIndex(s.Stage)
		if i < 0 || i < last {
			return &ExitError{20, "invalid_plan_order"}
		}
		last = i
		op, ok := passocontract.Operations[s.Operation]
		if !ok {
			return &ExitError{20, "unknown_operation"}
		}
		if operationStage(s.Operation) != s.Stage {
			return &ExitError{20, "operation_stage_mismatch"}
		}
		if !validParamTemplates(s.Params) {
			return &ExitError{20, "invalid_plan"}
		}
		if !validQueryTemplates(s.Query) {
			return &ExitError{20, "invalid_plan"}
		}
		humanAction := s.Operation == "seal:deployAuthorize" || s.Operation == "securitygate:confirmRemediationMaterialization"
		if op.Method != "GET" && s.Check == nil && !humanAction {
			return &ExitError{20, "mutation_check_required"}
		}
		if len(s.Input) > 0 && !validJSONObject(s.Input) {
			return &ExitError{20, "invalid_plan"}
		}
		if s.Check != nil {
			cop, cok := passocontract.Operations[s.Check.Operation]
			if !cok || cop.Method != http.MethodGet || s.Check.Pointer != "" && !validPointer(s.Check.Pointer) || !json.Valid(s.Check.Equals) {
				return &ExitError{20, "invalid_check"}
			}
			if operationStage(s.Check.Operation) != s.Stage {
				return &ExitError{20, "operation_stage_mismatch"}
			}
			if !validParamTemplates(s.Check.Params) {
				return &ExitError{20, "invalid_check"}
			}
			if !validQueryTemplates(s.Check.Query) {
				return &ExitError{20, "invalid_check"}
			}
		}
	}
	return nil
}

func validParamTemplates(params map[string]string) bool {
	for _, value := range params {
		if !validTemplate(value) {
			return false
		}
	}
	return true
}
func validQueryTemplates(query map[string][]string) bool {
	for _, values := range query {
		for _, value := range values {
			if !validTemplate(value) {
				return false
			}
		}
	}
	return true
}
func validTemplate(value string) bool {
	return !strings.Contains(value, "${") || value == "${projectId}" || value == "${versionId}" ||
		value == "${versionSequence}" || value == "${versionLabel}" || value == "${releaseId}" || value == "${runId}"
}
func planUsesVersionLabel(plan runPlan) bool {
	for _, step := range plan.Steps {
		if paramsUseVersionLabel(step.Params) || queryUsesVersionLabel(step.Query) {
			return true
		}
		if step.Check != nil && (paramsUseVersionLabel(step.Check.Params) || queryUsesVersionLabel(step.Check.Query)) {
			return true
		}
	}
	return false
}
func paramsUseVersionLabel(params map[string]string) bool {
	for _, value := range params {
		if value == "${versionLabel}" {
			return true
		}
	}
	return false
}
func queryUsesVersionLabel(query map[string][]string) bool {
	for _, values := range query {
		for _, value := range values {
			if value == "${versionLabel}" {
				return true
			}
		}
	}
	return false
}
func stageIndex(s string) int {
	for i, x := range runStages {
		if x == s {
			return i
		}
	}
	return -1
}
func operationStage(key string) string {
	prefix := strings.SplitN(key, ":", 2)[0]
	switch prefix {
	case "connect":
		return "connect"
	case "projects":
		return "connect"
	case "journey":
		return "monitor"
	case "vibe-docs-boardos", "documentation":
		return "docs"
	case "define":
		if strings.HasPrefix(key, "define:defineAnalysis") {
			return "discover"
		}
		if strings.HasPrefix(key, "define:documentation") {
			return "docs"
		}
		if strings.HasPrefix(key, "define:scope") {
			return "define"
		}
		return ""
	case "journeycontract":
		if strings.Contains(key, "release") && !strings.Contains(key, "managedDeployment") {
			return "freeze"
		}
		return "deploy"
	case "securitygate", "securityscan":
		return "secure"
	case "architecture":
		return "design"
	case "regioncompare", "regioncompare.v2":
		return "compare"
	case "seal":
		return "seal"
	case "deployment", "environment":
		return "deploy"
	}
	return ""
}
func validJSONObject(b []byte) bool {
	var x map[string]json.RawMessage
	if json.Unmarshal(b, &x) != nil || x == nil {
		return false
	}
	return true
}
func substituteParams(src map[string]string, r *Runtime) map[string]string {
	out := map[string]string{}
	for k, v := range src {
		switch v {
		case "${projectId}":
			v = strconv.FormatInt(r.ProjectID, 10)
		case "${versionId}":
			v = r.VersionID
		case "${versionLabel}":
			v = r.VersionLabel
		case "${versionSequence}":
			if r.VersionSequence > 0 {
				v = strconv.FormatInt(r.VersionSequence, 10)
			} else {
				v = ""
			}
		case "${releaseId}":
			v = r.ReleaseID
		case "${runId}":
			v = r.RunID
		}
		out[k] = v
	}
	return out
}
func substituteValues(src map[string][]string, r *Runtime) url.Values {
	out := url.Values{}
	for key, values := range src {
		for _, value := range values {
			switch value {
			case "${projectId}":
				value = strconv.FormatInt(r.ProjectID, 10)
			case "${versionId}":
				value = r.VersionID
			case "${versionLabel}":
				value = r.VersionLabel
			case "${versionSequence}":
				if r.VersionSequence > 0 {
					value = strconv.FormatInt(r.VersionSequence, 10)
				} else {
					value = ""
				}
			case "${releaseId}":
				value = r.ReleaseID
			case "${runId}":
				value = r.RunID
			}
			out.Add(key, value)
		}
	}
	return out
}
func checkRun(ctx context.Context, r *Runtime, stage string, c runCheck, p map[string]string, q url.Values) (bool, json.RawMessage, error) {
	res, e := r.Perform(ctx, stage, c.Operation, p, q, nil, false)
	if e != nil {
		return false, nil, e
	}
	got, ok := jsonPointer(res.Data, c.Pointer)
	if !ok {
		return false, res.Data, nil
	}
	a, e := canonicalJSON(got)
	if e != nil {
		return false, nil, &ExitError{30, "invalid_check_response"}
	}
	b, e := canonicalJSON(c.Equals)
	if e != nil {
		return false, nil, &ExitError{20, "invalid_check"}
	}
	return bytes.Equal(a, b), res.Data, nil
}
func canonicalJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	return marshalCanonical(v)
}

func marshalCanonical(v any) ([]byte, error) {
	switch x := v.(type) {
	case json.Number:
		return []byte(normalizeJSONNumber(string(x))), nil
	case []any:
		parts := make([]json.RawMessage, len(x))
		for i, item := range x {
			b, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			parts[i] = b
		}
		return json.Marshal(parts)
	case map[string]any:
		out := make(map[string]json.RawMessage, len(x))
		for k, item := range x {
			b, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			out[k] = b
		}
		return json.Marshal(out)
	default:
		return json.Marshal(v)
	}
}

// normalizeJSONNumber returns a stable decimal spelling without changing the
// exact value. JSON permits equivalent forms such as 1, 1.0 and 10e-1.
func normalizeJSONNumber(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	exponent := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, _ = strconv.Atoi(s[i+1:])
		s = s[:i]
	}
	point := len(s)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		point = i
		s = s[:i] + s[i+1:]
	}
	point += exponent
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
		point--
	}
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if strings.Trim(s, "0") == "" {
		return "0"
	}
	if point <= 0 {
		return sign + "0." + strings.Repeat("0", -point) + s
	}
	if point >= len(s) {
		return sign + s + strings.Repeat("0", point-len(s))
	}
	return sign + s[:point] + "." + s[point:]
}
func validPointer(p string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for _, x := range strings.Split(p[1:], "/") {
		for i := 0; i < len(x); i++ {
			if x[i] == '~' && (i+1 >= len(x) || (x[i+1] != '0' && x[i+1] != '1')) {
				return false
			}
			if x[i] == '~' {
				i++
			}
		}
	}
	return true
}
func jsonPointer(raw []byte, p string) (json.RawMessage, bool) {
	var cur any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&cur) != nil {
		return nil, false
	}
	if p != "" {
		for _, part := range strings.Split(p[1:], "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch v := cur.(type) {
			case map[string]any:
				var ok bool
				cur, ok = v[part]
				if !ok {
					return nil, false
				}
			case []any:
				i, e := strconv.Atoi(part)
				if e != nil || i < 0 || i >= len(v) || strconv.Itoa(i) != part {
					return nil, false
				}
				cur = v[i]
			default:
				return nil, false
			}
		}
	}
	b, e := json.Marshal(cur)
	return b, e == nil
}
func runContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &ExitError{20, "run_cancelled"}
	}
	return &ExitError{20, "run_timeout"}
}

func runCallError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return runContextError(ctx.Err())
	}
	return err
}

var _ = time.Second
