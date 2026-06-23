package board

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// mcp.go — a minimal streamable-HTTP MCP client, just enough for the CLI to drive the board.
//
// It speaks the MCP JSON-RPC handshake (initialize -> notifications/initialized -> tools/call) over
// a single HTTP endpoint, and parses both plain-JSON and text/event-stream (SSE) responses, exactly
// like the reference board helper. It deliberately depends only on the stdlib so the CLI keeps its
// tiny dependency surface.

// DefaultMCPURL is the passo.build board MCP endpoint. Override with --board-url or PYX_BOARD_MCP_URL.
const DefaultMCPURL = "https://mcp.passo.build/mcp"

const mcpProtocolVersion = "2025-06-18"

// Client is an MCP JSON-RPC client bound to one endpoint + bearer token.
type Client struct {
	URL        string
	Token      string
	HTTPClient *http.Client
	sessionID  string
}

// NewClient builds an MCP client. An empty url falls back to DefaultMCPURL.
func NewClient(url, token string) *Client {
	if strings.TrimSpace(url) == "" {
		url = DefaultMCPURL
	}
	return &Client{
		URL:        url,
		Token:      token,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// rpc performs one JSON-RPC call (or notification when notify=true) and returns the raw result.
func (c *Client) rpc(method string, params any, id int, notify bool) (json.RawMessage, error) {
	req := rpcRequest{JSONRPC: "2.0", Method: method}
	if !notify {
		req.ID = id
	}
	if params != nil {
		req.Params = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequest("POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("board auth failed (HTTP %d) — run `pyx auth login` or check --board-url/token", resp.StatusCode)
	}
	if notify {
		return nil, nil
	}
	decoded, err := decodeEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil {
		return nil, decoded.Error
	}
	return decoded.Result, nil
}

// decodeEnvelope parses a JSON-RPC response from either a plain JSON body or an SSE stream (it
// returns the last data: frame, mirroring the reference helper).
func decodeEnvelope(raw []byte) (*rpcResponse, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var r rpcResponse
		if err := json.Unmarshal(trimmed, &r); err == nil {
			return &r, nil
		}
	}
	var last *rpcResponse
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(payload), &r); err == nil {
			rr := r
			last = &rr
		}
	}
	if last == nil {
		return nil, fmt.Errorf("no JSON-RPC payload in response: %s", truncate(string(raw), 300))
	}
	return last, nil
}

// ensureSession runs the MCP handshake once per client.
func (c *Client) ensureSession() error {
	if c.sessionID != "" {
		return nil
	}
	_, err := c.rpc("initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "pyxcloud-cli", "version": "1.0"},
	}, 1, false)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if _, err := c.rpc("notifications/initialized", nil, 0, true); err != nil {
		return fmt.Errorf("initialized notify: %w", err)
	}
	return nil
}

// toolResult is the shape of an MCP tools/call result we consume.
type toolResult struct {
	Content           []contentBlock `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
	IsError           bool           `json:"isError"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolCallResult is what CallTool returns to the command layer: the structured payload (for
// view-spec projection) plus the first text block (the human/JSON fallback).
type ToolCallResult struct {
	Structured map[string]any
	Text       string
	IsError    bool
}

// CallTool invokes one MCP tool by name with the given arguments.
func (c *Client) CallTool(name string, args map[string]any) (*ToolCallResult, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.rpc("tools/call", map[string]any{"name": name, "arguments": args}, 2, false)
	if err != nil {
		return nil, err
	}
	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}
	out := &ToolCallResult{Structured: tr.StructuredContent, IsError: tr.IsError}
	for _, b := range tr.Content {
		if b.Type == "text" && b.Text != "" {
			out.Text = b.Text
			break
		}
	}
	// Some tools embed the structured payload as a JSON text block instead of structuredContent;
	// surface it as Structured too so the view-spec projector can find a spec there.
	if out.Structured == nil && out.Text != "" {
		var m map[string]any
		if json.Unmarshal([]byte(out.Text), &m) == nil {
			out.Structured = m
		}
	}
	return out, nil
}

// ListToolNames returns the set of tool names the server exposes, so the CLI can detect whether the
// live manifest tool is available before relying on it.
func (c *Client) ListToolNames() (map[string]bool, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	raw, err := c.rpc("tools/list", map[string]any{}, 3, false)
	if err != nil {
		return nil, err
	}
	var lst struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &lst); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	names := make(map[string]bool, len(lst.Tools))
	for _, t := range lst.Tools {
		names[t.Name] = true
	}
	return names, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
