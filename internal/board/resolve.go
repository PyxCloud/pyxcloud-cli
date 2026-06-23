package board

import "encoding/json"

// ResolveManifest returns the action manifest the CLI should drive its commands from. It PREFERS
// the live manifest emitted by the passobuild_board_action_manifest MCP tool (so a server-side
// change reaches the CLI without a rebuild) and DEGRADES GRACEFULLY to the vendored copy when the
// tool is absent (deploy pending) or the call fails. The bool reports whether the live manifest was
// used, so the caller can hint the source to the user.
func ResolveManifest(c *Client) (actions []BoardAction, live bool) {
	if c == nil {
		return VendoredManifest(), false
	}
	names, err := c.ListToolNames()
	if err != nil || !names["passobuild_board_action_manifest"] {
		return VendoredManifest(), false
	}
	res, err := c.CallTool("passobuild_board_action_manifest", nil)
	if err != nil || res.Structured == nil {
		return VendoredManifest(), false
	}
	raw, ok := res.Structured["actions"]
	if !ok {
		return VendoredManifest(), false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return VendoredManifest(), false
	}
	var parsed []BoardAction
	if err := json.Unmarshal(b, &parsed); err != nil || len(parsed) == 0 {
		return VendoredManifest(), false
	}
	return parsed, true
}
