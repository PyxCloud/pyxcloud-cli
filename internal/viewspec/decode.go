package viewspec

import (
	"encoding/json"
	"fmt"
)

// FromMap decodes an untyped view-spec (as it arrives inside a tool's structuredContent under the
// "viewSpec" key) into a typed ViewSpec. It round-trips through JSON so it tolerates the loose
// numeric typing of decoded JSON (float64 for ints) and ignores unknown fields, keeping the CLI
// forward-compatible if the server adds spec fields the CLI does not yet render.
func FromMap(m map[string]any) (*ViewSpec, error) {
	if m == nil {
		return nil, fmt.Errorf("viewspec: nil map")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("viewspec: marshal: %w", err)
	}
	var v ViewSpec
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("viewspec: decode: %w", err)
	}
	return &v, nil
}

// Extract pulls a ViewSpec out of an arbitrary structuredContent payload. The board nests the spec
// under the "viewSpec" key; some tools may return the spec at the top level. Extract handles both,
// and returns (nil, false) when no spec is present so the caller can DEGRADE GRACEFULLY (fall back
// to its own rendering) rather than error — the live MCP may not emit the spec yet (deploy pending).
func Extract(structured map[string]any) (*ViewSpec, bool) {
	if structured == nil {
		return nil, false
	}
	if raw, ok := structured["viewSpec"]; ok {
		if mm, ok := raw.(map[string]any); ok {
			if v, err := FromMap(mm); err == nil && hasContent(v) {
				return v, true
			}
		}
	}
	// Top-level spec (the payload IS the spec).
	if _, ok := structured["sections"]; ok {
		if v, err := FromMap(structured); err == nil && hasContent(v) {
			return v, true
		}
	}
	return nil, false
}

// hasContent reports whether a decoded spec carries anything worth projecting, so a malformed or
// empty "viewSpec" key does not suppress the caller's fallback rendering.
func hasContent(v *ViewSpec) bool {
	return v != nil && (v.Title != "" || len(v.Sections) > 0)
}
