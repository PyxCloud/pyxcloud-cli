package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/pyxcloud/pyxcloud-cli/internal/board"
	"github.com/pyxcloud/pyxcloud-cli/internal/viewspec"
	"github.com/spf13/cobra"
)

// board.go — `pyx board <action>` commands GENERATED from the canonical action manifest, routed via
// MCP. This closes the CLI deploy-gate bypass: instead of the CLI hitting backend endpoints that
// skip the board's gates, every board action goes through the SAME MCP tool the agent and FE use, so
// the board enforces lease/approval/verify/decompose/optimize/security uniformly across surfaces.
//
// The command tree is built at init() from board.VendoredManifest() (the compiled-in mirror of the
// contract) so `pyx board --help` enumerates the real vocabulary offline. At RUN time the CLI
// re-resolves the manifest against the LIVE server (passobuild_board_action_manifest) and prefers
// it, falling back to the vendored copy if the tool is not deployed yet.

var boardURL string
var boardToken string

var boardCmd = &cobra.Command{
	Use:   "board",
	Short: "Drive passo.build board actions (routed via MCP, gate-enforced)",
	Long: `Board commands mirror the canonical action manifest shared by the agent, the web
control-hub, and this CLI, so all three stay coherent. Each action is routed through the
same MCP tool the agent uses, so the board enforces its gates uniformly.

  pyx board actions                       list the action vocabulary
  pyx board status   --project <id>       project the board view-spec as text
  pyx board task.next --project <id>
  pyx board task.claim --taskId <id> --project <id> --agentId <me>`,
}

// boardActionsCmd enumerates the resolved manifest (live-preferred) as a table.
var boardActionsCmd = &cobra.Command{
	Use:   "actions",
	Short: "List the board action vocabulary (live manifest preferred)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := boardClient()
		if err != nil {
			return err
		}
		actions, live := board.ResolveManifest(client)
		source := "vendored (live manifest tool not deployed yet)"
		if live {
			source = "live MCP manifest"
		}
		fmt.Printf("Action manifest source: %s\n\n", source)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ACTION\tMCP TOOL\tGATES\tKIND")
		fmt.Fprintln(w, "------\t--------\t-----\t----")
		for _, a := range actions {
			kind := "canonical"
			if a.Recovery {
				kind = "recovery"
			} else if !a.Canonical {
				kind = "alt"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.MCPTool, gatesString(a.Gates), kind)
		}
		return w.Flush()
	},
}

// boardStatusCmd projects the board status view-spec as terminal text (the U8 projector wired to a
// real board tool). It is a friendly alias for the passobuild_board_status tool.
var boardStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Project the board status view-spec as text",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		if projectID == "" {
			return fmt.Errorf("--project is required")
		}
		client, err := boardClient()
		if err != nil {
			return err
		}
		res, err := client.CallTool("passobuild_board_status", map[string]any{
			"projectId":    projectID,
			"editor":       "cli",
			"widgetFormat": "none",
		})
		if err != nil {
			return fmt.Errorf("board status: %w", err)
		}
		renderToolResult(res)
		return nil
	},
}

// newActionCmd builds a cobra command for one manifest action: a flag per argSchema entry, routed
// to the action's MCP tool. This is the generation step — every action becomes a CLI command with
// the SAME arguments the contract declares.
func newActionCmd(a board.BoardAction) *cobra.Command {
	c := &cobra.Command{
		Use:   a.Name,
		Short: a.Description,
		Long:  fmt.Sprintf("%s\n\nMCP tool: %s\nGates: %s", a.Description, a.MCPTool, gatesString(a.Gates)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := boardClient()
			if err != nil {
				return err
			}
			// Re-resolve live so a renamed/added tool is honored at run time.
			actions, _ := board.ResolveManifest(client)
			resolved, ok := board.FindAction(actions, a.Name)
			if !ok {
				resolved = a // fall back to the build-time action
			}
			callArgs := collectArgs(cmd, resolved)
			res, err := client.CallTool(resolved.MCPTool, callArgs)
			if err != nil {
				return fmt.Errorf("%s: %w", a.Name, err)
			}
			renderToolResult(res)
			if res.IsError {
				return fmt.Errorf("%s returned an error result (see above)", a.Name)
			}
			return nil
		},
	}
	for _, key := range argKeysOrdered(a) {
		c.Flags().String(key, "", a.ArgSchema[key])
	}
	return c
}

// collectArgs reads the action's declared flags into an arguments map, JSON-coercing values where
// the contract expects non-strings (numbers, bools, arrays) so the MCP tool receives well-typed args.
func collectArgs(cmd *cobra.Command, a board.BoardAction) map[string]any {
	out := map[string]any{}
	for _, key := range argKeysOrdered(a) {
		v, _ := cmd.Flags().GetString(key)
		if v == "" {
			continue
		}
		out[key] = coerce(key, v)
	}
	return out
}

// coerce turns a string flag value into the JSON type the contract implies from the arg name.
func coerce(key, v string) any {
	switch key {
	case "complexity":
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	case "fenceToken":
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	case "requiresApproval":
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	case "deps", "evidence":
		// Accept JSON arrays or comma-separated lists.
		var arr []any
		if json.Unmarshal([]byte(v), &arr) == nil {
			return arr
		}
		parts := strings.Split(v, ",")
		list := make([]any, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				list = append(list, p)
			}
		}
		return list
	}
	return v
}

// renderToolResult prints a tool result: PREFER the view-spec text projection (single-source view),
// else fall back to the tool's text/JSON block. Degrades gracefully when no spec is present.
func renderToolResult(res *board.ToolCallResult) {
	if res == nil {
		return
	}
	if spec, ok := viewspec.Extract(res.Structured); ok {
		fmt.Print(spec.ProjectText())
		return
	}
	if res.Text != "" {
		fmt.Println(strings.TrimRight(res.Text, "\n"))
		return
	}
	if res.Structured != nil {
		b, _ := json.MarshalIndent(res.Structured, "", "  ")
		fmt.Println(string(b))
	}
}

func gatesString(gates []board.ActionGate) string {
	if len(gates) == 0 {
		return "-"
	}
	s := make([]string, len(gates))
	for i, g := range gates {
		s[i] = string(g)
	}
	return strings.Join(s, ",")
}

// argKeysOrdered returns the action's argument keys in the canonical ArgOrder, then any remaining
// schema keys sorted (Go map order is not stable).
func argKeysOrdered(a board.BoardAction) []string {
	seen := map[string]bool{}
	var keys []string
	for _, k := range a.ArgOrder {
		if _, ok := a.ArgSchema[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range a.ArgSchema {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// boardClient builds an MCP client from flags/env/config. The board lives on the passo.build MCP
// (passobuild realm), distinct from the pyxcloud deploy backend, so it has its own URL/token knobs.
func boardClient() (*board.Client, error) {
	url := boardURL
	if url == "" {
		url = os.Getenv("PYX_BOARD_MCP_URL")
	}
	token := boardToken
	if token == "" {
		token = os.Getenv("PYX_BOARD_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("no board token: set --board-token or PYX_BOARD_TOKEN (board uses the passo.build MCP)")
	}
	return board.NewClient(url, token), nil
}

func init() {
	boardCmd.PersistentFlags().StringVar(&boardURL, "board-url", "", "Board MCP endpoint (default https://mcp.passo.build/mcp; or PYX_BOARD_MCP_URL)")
	boardCmd.PersistentFlags().StringVar(&boardToken, "board-token", "", "Board MCP bearer token (or PYX_BOARD_TOKEN)")

	boardStatusCmd.Flags().StringP("project", "p", "", "Project ID (required)")

	boardCmd.AddCommand(boardActionsCmd)
	boardCmd.AddCommand(boardStatusCmd)
	// Generate one command per canonical/recovery action from the vendored manifest mirror.
	for _, a := range board.VendoredManifest() {
		boardCmd.AddCommand(newActionCmd(a))
	}
	rootCmd.AddCommand(boardCmd)
}
