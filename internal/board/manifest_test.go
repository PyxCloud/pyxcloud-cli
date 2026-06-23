package board

import (
	"encoding/json"
	"testing"
)

// TestVendoredManifest_MirrorsContract pins the vendored manifest to the skill-plugin contract:
// the canonical close is task.complete (canonical), reconcile is recovery-only, and the
// complete-vs-reconcile mismatch stays resolved.
func TestVendoredManifest_MirrorsContract(t *testing.T) {
	m := VendoredManifest()
	if len(m) != 9 {
		t.Fatalf("expected 9 actions (contract), got %d", len(m))
	}

	complete, ok := FindAction(m, "task.complete")
	if !ok {
		t.Fatal("task.complete missing")
	}
	if complete.MCPTool != "passobuild_board_task_complete" || !complete.Canonical {
		t.Errorf("task.complete must be canonical -> passobuild_board_task_complete, got %+v", complete)
	}
	if !hasGate(complete.Gates, GateLease) || !hasGate(complete.Gates, GateVerify) || !hasGate(complete.Gates, GateOptimize) {
		t.Errorf("task.complete must carry lease+verify+optimize gates, got %v", complete.Gates)
	}

	rec, ok := FindAction(m, "task.reconcile")
	if !ok {
		t.Fatal("task.reconcile missing")
	}
	if rec.Canonical || !rec.Recovery {
		t.Errorf("task.reconcile must be recovery-only (canonical=false, recovery=true), got %+v", rec)
	}
	if rec.MCPTool != "passobuild_board_task_reconcile" {
		t.Errorf("task.reconcile must route to passobuild_board_task_reconcile, got %q", rec.MCPTool)
	}
}

func TestVendoredManifest_IsCopy(t *testing.T) {
	m := VendoredManifest()
	m[0].Name = "mutated"
	if VendoredManifest()[0].Name == "mutated" {
		t.Error("VendoredManifest must return a copy, not the package slice")
	}
}

// TestBoardAction_JSONRoundTrips proves the struct tags match the live manifest wire shape, so the
// SAME struct decodes the passobuild_board_action_manifest tool output.
func TestBoardAction_JSONRoundTrips(t *testing.T) {
	wire := `{
      "name":"task.claim","title":"Claim task","mcpTool":"passobuild_board_task_claim",
      "argSchema":{"taskId":"task to claim"},"argOrder":["taskId"],
      "gates":["approval"],"canonical":true,"description":"Lease a ready task."
    }`
	var a BoardAction
	if err := json.Unmarshal([]byte(wire), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if a.Name != "task.claim" || a.MCPTool != "passobuild_board_task_claim" || !a.Canonical {
		t.Errorf("decoded action mismatch: %+v", a)
	}
	if len(a.Gates) != 1 || a.Gates[0] != GateApproval {
		t.Errorf("gate decode mismatch: %v", a.Gates)
	}
}

func hasGate(gs []ActionGate, want ActionGate) bool {
	for _, g := range gs {
		if g == want {
			return true
		}
	}
	return false
}
