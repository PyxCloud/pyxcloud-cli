// Package viewspec is the CLI consumer of the board VIEW-SPEC contract.
//
// The board MCP rides a structured "viewSpec" block alongside the rendered widgetHtml in every
// board tool's structuredContent. The view-spec is the canonical STRUCTURED projection of the
// SAME data the HTML shows (a superset, never a subset), so a text-only host — Claude Code's
// terminal, this CLI — can TEXT-PROJECT the identical view without a browser. That is what makes
// the board UI single-source across agent/FE/CLI.
//
// The types and ProjectText() logic here mirror, field-for-field, the contract in the skill-plugin
// repo (mcp-go/widgetkit/viewspec.go) so the CLI projection is byte-compatible with the server's
// own ProjectText(). The CLI consumes the spec as untyped JSON (map[string]any) because it arrives
// over the wire inside structuredContent; FromMap() decodes that into the typed ViewSpec below.
package viewspec

import (
	"fmt"
	"strings"
)

// SectionKind enumerates the widgetkit primitives a section can be (1:1 with the contract).
type SectionKind string

const (
	KindBanner  SectionKind = "banner"
	KindMetrics SectionKind = "metrics"
	KindTable   SectionKind = "table"
	KindList    SectionKind = "list"
	KindKV      SectionKind = "kv"
	KindBars    SectionKind = "bars"
	KindBadge   SectionKind = "badge"
)

// Metric is one label/value/tone card (mirror of widgetkit.Metric).
type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  string `json:"tone,omitempty"`
}

// Cell is one table cell (mirror of widgetkit.Cell).
type Cell struct {
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

// KV is one key/value row (mirror of widgetkit.KV).
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Bar is one labelled progress row (mirror of widgetkit.Bar).
type Bar struct {
	Label string `json:"label"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// ViewSection is one ordered block of a view. Only the field(s) for its Kind are populated; the
// rest are zero (a tagged-union-as-struct, so it JSON-marshals flat and we switch on Kind).
type ViewSection struct {
	Kind SectionKind `json:"kind"`

	// Banner: State is ok/warn/danger/info; Title + Text are the two lines. Badge reuses Title+State.
	State string `json:"state,omitempty"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`

	Metrics []Metric `json:"metrics,omitempty"`

	Headers []string `json:"headers,omitempty"`
	Rows    [][]Cell `json:"rows,omitempty"`

	Items []string `json:"items,omitempty"`

	KVs []KV `json:"kvs,omitempty"`

	Bars []Bar `json:"bars,omitempty"`
}

// ViewSpec is the ordered, structured view: a title, a one-line summary, and the ordered sections.
type ViewSpec struct {
	Title    string        `json:"title,omitempty"`
	Summary  string        `json:"summary,omitempty"`
	Sections []ViewSection `json:"sections"`
}

// ProjectText renders the view-spec to a deterministic plain-text projection for a terminal/CLI
// host — the SAME view as the widgetHtml, in text. No HTML, no color codes. This mirrors the
// server's widgetkit.ViewSpec.ProjectText() so CLI output matches the contract exactly.
func (v *ViewSpec) ProjectText() string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(v.Title) != "" {
		fmt.Fprintf(&b, "%s\n%s\n", v.Title, strings.Repeat("=", len(v.Title)))
	}
	for _, s := range v.Sections {
		switch s.Kind {
		case KindBanner:
			fmt.Fprintf(&b, "[%s] %s — %s\n", strings.ToUpper(orPlain(s.State, "info")), s.Title, s.Text)
		case KindBadge:
			fmt.Fprintf(&b, "(%s) %s\n", orPlain(s.State, "info"), s.Title)
		case KindMetrics:
			for _, m := range s.Metrics {
				fmt.Fprintf(&b, "  %-22s %s\n", m.Label+":", m.Value)
			}
		case KindKV:
			for _, kv := range s.KVs {
				fmt.Fprintf(&b, "  %-22s %s\n", kv.Key+":", kv.Value)
			}
		case KindList:
			for _, it := range s.Items {
				fmt.Fprintf(&b, "  - %s\n", it)
			}
		case KindBars:
			for _, bar := range s.Bars {
				fmt.Fprintf(&b, "  %-22s %d/%d\n", bar.Label+":", bar.Done, bar.Total)
			}
		case KindTable:
			b.WriteString(projectTable(s.Headers, s.Rows))
		}
	}
	return b.String()
}

// projectTable renders a column-aligned text table (mirror of the contract's projectTable).
func projectTable(headers []string, rows [][]Cell) string {
	cols := len(headers)
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return ""
	}
	widths := make([]int, cols)
	for i, h := range headers {
		if len(h) > widths[i] {
			widths[i] = len(h)
		}
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c.Text) > widths[i] {
				widths[i] = len(c.Text)
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("  ")
		for i := 0; i < cols; i++ {
			val := ""
			if i < len(cells) {
				val = cells[i]
			}
			fmt.Fprintf(&b, "%-*s", widths[i]+2, val)
		}
		b.WriteString("\n")
	}
	if len(headers) > 0 {
		writeRow(headers)
		sep := make([]string, cols)
		for i := range sep {
			sep[i] = strings.Repeat("-", widths[i])
		}
		writeRow(sep)
	}
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = c.Text
		}
		writeRow(cells)
	}
	return b.String()
}

func orPlain(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
