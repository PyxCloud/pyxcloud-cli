// Package board is the CLI consumer of the board ACTION MANIFEST contract.
//
// The manifest is the SINGLE source of truth for "what actions exist on a passo.build project",
// shared by the three surfaces (agent, FE, CLI) so they never drift: the same
// {name, mcpTool, argSchema, gates} contract drives the agent's tool calls, the FE control-hub
// buttons, and these CLI commands. It lives next to board_os.go in the skill-plugin repo
// (mcp-go/action_manifest.go); this file mirrors that contract so `pyx board` commands are
// generated from the SAME vocabulary.
//
// At runtime the CLI PREFERS the live manifest emitted by the passobuild_board_action_manifest MCP
// tool (so a server-side manifest change reaches the CLI without a rebuild). When the live MCP does
// not yet expose that tool (deploy pending), the CLI DEGRADES GRACEFULLY to this vendored copy, so
// `pyx board` is usable today and self-updates the moment the tool ships.
package board

// ActionGate is one precondition the board enforces before an action can succeed (mirror of the
// contract's ActionGate). Surfaces use these to explain WHY a control is gated.
type ActionGate string

const (
	GateLease       ActionGate = "lease"
	GateApproval    ActionGate = "approval"
	GateVerify      ActionGate = "independent-verify"
	GateDecompose   ActionGate = "decomposition"
	GateOptimize    ActionGate = "optimization"
	GateSecurity    ActionGate = "security"
	GateSpend       ActionGate = "spend"
	GateDestructive ActionGate = "destructive"
)

// BoardAction is one canonical action (mirror of the contract's BoardAction). Field tags match the
// JSON the passobuild_board_action_manifest tool emits, so the same struct decodes the live manifest.
type BoardAction struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	MCPTool     string            `json:"mcpTool"`
	ArgSchema   map[string]string `json:"argSchema"`
	ArgOrder    []string          `json:"argOrder"`
	Gates       []ActionGate      `json:"gates"`
	Canonical   bool              `json:"canonical"`
	Recovery    bool              `json:"recovery,omitempty"`
	Description string            `json:"description"`
}

// vendoredActions mirrors skill-plugin mcp-go/action_manifest.go:boardActions verbatim (same order,
// same names, same gates). Used as the fallback when the live manifest tool is unavailable.
var vendoredActions = []BoardAction{
	{
		Name:    "task.create",
		Title:   "Create task",
		MCPTool: "passobuild_board_task_create",
		ArgSchema: map[string]string{
			"projectId":        "project the task belongs to",
			"title":            "task title",
			"complexity":       "AI Context Units 1-6 (tasks > 6 must be split)",
			"deps":             "ids of tasks this one depends on",
			"repoScope":        "repo this task touches (deconfliction)",
			"requiresApproval": "true for C4/global or risk-gated work",
			"parentId":         "macro task this is a subtask of (rule 7/8)",
		},
		ArgOrder:    []string{"projectId", "title", "complexity", "deps", "repoScope", "requiresApproval", "parentId"},
		Canonical:   true,
		Description: "Create or replace a task in the shared plan.",
	},
	{
		Name:        "task.next",
		Title:       "Next ready task",
		MCPTool:     "passobuild_board_task_next",
		ArgSchema:   map[string]string{"projectId": "project to pull the frontier from"},
		ArgOrder:    []string{"projectId"},
		Canonical:   true,
		Description: "Return the next claimable task on the DAG frontier.",
	},
	{
		Name:    "task.claim",
		Title:   "Claim task",
		MCPTool: "passobuild_board_task_claim",
		ArgSchema: map[string]string{
			"taskId":    "task to claim",
			"projectId": "project the task belongs to",
			"agentId":   "claiming agent id",
		},
		ArgOrder:    []string{"taskId", "projectId", "agentId"},
		Gates:       []ActionGate{GateApproval},
		Canonical:   true,
		Description: "Lease a ready task for execution (returns a fence token).",
	},
	{
		Name:    "task.heartbeat",
		Title:   "Heartbeat",
		MCPTool: "passobuild_board_task_heartbeat",
		ArgSchema: map[string]string{
			"taskId":     "leased task",
			"fenceToken": "fence token from the claim",
		},
		ArgOrder:    []string{"taskId", "fenceToken"},
		Gates:       []ActionGate{GateLease},
		Canonical:   true,
		Description: "Renew the lease on a claimed task so it is not reaped.",
	},
	{
		Name:    "task.update",
		Title:   "Update task",
		MCPTool: "passobuild_board_task_update",
		ArgSchema: map[string]string{
			"taskId":     "leased task",
			"fenceToken": "fence token from the claim",
			"status":     "new status",
		},
		ArgOrder:    []string{"taskId", "fenceToken", "status"},
		Gates:       []ActionGate{GateLease},
		Canonical:   true,
		Description: "Update a claimed task's fields under its lease.",
	},
	{
		Name:    "task.verify",
		Title:   "Independent verify",
		MCPTool: "passobuild_board_task_verify",
		ArgSchema: map[string]string{
			"taskId":  "task under review",
			"verdict": "PASS or FAIL from a reviewer who is NOT the implementer",
			"note":    "review evidence",
		},
		ArgOrder:    []string{"taskId", "verdict", "note"},
		Canonical:   true,
		Description: "Record an independent reviewer's PASS/FAIL (satisfies the verify gate).",
	},
	{
		Name:    "task.complete",
		Title:   "Complete task",
		MCPTool: "passobuild_board_task_complete",
		ArgSchema: map[string]string{
			"taskId":              "leased task to close",
			"fenceToken":          "fence token from the claim",
			"evidence":            "evidence rows (incl. the recorded optimization)",
			"atomicJustification": "assert the macro task is irreducibly atomic (decomposition gate)",
			"optimizationWaiver":  "assert no optimization applies (optimization gate)",
		},
		ArgOrder:    []string{"taskId", "fenceToken", "evidence", "atomicJustification", "optimizationWaiver"},
		Gates:       []ActionGate{GateLease, GateApproval, GateVerify, GateDecompose, GateOptimize},
		Canonical:   true,
		Description: "Close a claimed task (the canonical complete; enforces the full close gates).",
	},
	{
		Name:    "task.release",
		Title:   "Release task",
		MCPTool: "passobuild_board_task_release",
		ArgSchema: map[string]string{
			"taskId":     "leased task",
			"fenceToken": "fence token from the claim",
		},
		ArgOrder:    []string{"taskId", "fenceToken"},
		Gates:       []ActionGate{GateLease},
		Canonical:   true,
		Description: "Drop the lease without completing (returns the task to the frontier).",
	},
	{
		Name:    "task.reconcile",
		Title:   "Recover task (admin)",
		MCPTool: "passobuild_board_task_reconcile",
		ArgSchema: map[string]string{
			"taskId":   "stuck/orphaned task (IN-PROGRESS, dead holder)",
			"action":   "'reopen' (drop lease -> TODO) or 'complete' (force DONE)",
			"reason":   "required audit reason",
			"evidence": "evidence rows for action='complete'",
		},
		ArgOrder:    []string{"taskId", "action", "reason", "evidence"},
		Gates:       []ActionGate{GateVerify, GateDecompose, GateOptimize},
		Canonical:   false,
		Recovery:    true,
		Description: "Admin escape hatch for a stuck task with a dead lease; NOT a synonym for complete — reconcile-complete still enforces the verify/decompose/optimize gates.",
	},
}

// VendoredManifest returns the compiled-in fallback manifest (mirror of the contract).
func VendoredManifest() []BoardAction {
	out := make([]BoardAction, len(vendoredActions))
	copy(out, vendoredActions)
	return out
}

// FindAction returns the action with the given stable name, from the supplied manifest.
func FindAction(actions []BoardAction, name string) (BoardAction, bool) {
	for _, a := range actions {
		if a.Name == name {
			return a, true
		}
	}
	return BoardAction{}, false
}
