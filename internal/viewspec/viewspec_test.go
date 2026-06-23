package viewspec

import (
	"strings"
	"testing"
)

func TestProjectText_AllSectionKinds(t *testing.T) {
	v := &ViewSpec{
		Title:   "Project Alpha",
		Summary: "one-line summary",
		Sections: []ViewSection{
			{Kind: KindBanner, State: "warn", Title: "Needs attention", Text: "missing owner"},
			{Kind: KindMetrics, Metrics: []Metric{{Label: "Total", Value: "444"}, {Label: "Done", Value: "380", Tone: "ok"}}},
			{Kind: KindKV, KVs: []KV{{Key: "Owner", Value: "sp-cli"}}},
			{Kind: KindList, Items: []string{"first", "second"}},
			{Kind: KindBars, Bars: []Bar{{Label: "Progress", Done: 3, Total: 4}}},
			{Kind: KindBadge, Title: "beta", State: "info"},
			{Kind: KindTable, Headers: []string{"ID", "STATUS"}, Rows: [][]Cell{
				{{Text: "T1"}, {Text: "DONE"}},
				{{Text: "T2"}, {Text: "TODO"}},
			}},
		},
	}
	out := v.ProjectText()

	wants := []string{
		"Project Alpha",
		"=============", // title underline length == len(title)
		"[WARN] Needs attention — missing owner",
		"Total:                 444", // metric label padded to 22
		"(info) beta",                // badge
		"  - first",
		"Progress:              3/4",
		"ID", "STATUS", "T1", "DONE",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("projection missing %q\n---\n%s", w, out)
		}
	}
}

func TestProjectText_DefaultBannerState(t *testing.T) {
	v := &ViewSpec{Sections: []ViewSection{{Kind: KindBanner, Title: "t", Text: "x"}}}
	if !strings.Contains(v.ProjectText(), "[INFO]") {
		t.Errorf("empty banner state should default to INFO, got: %s", v.ProjectText())
	}
}

func TestProjectText_Nil(t *testing.T) {
	var v *ViewSpec
	if v.ProjectText() != "" {
		t.Error("nil spec must project empty string")
	}
}

func TestProjectText_Deterministic(t *testing.T) {
	v := &ViewSpec{Title: "X", Sections: []ViewSection{
		{Kind: KindMetrics, Metrics: []Metric{{Label: "A", Value: "1"}, {Label: "B", Value: "2"}}},
	}}
	if v.ProjectText() != v.ProjectText() {
		t.Error("projection must be deterministic")
	}
}

// TestFromMap_CapitalizedPrimitiveKeys proves the decoder tolerates the on-the-wire shape: the
// server's widgetkit primitives (Metric/Cell/KV/Bar) carry NO json tags, so they serialize with
// capitalized keys ("Label"/"Value"), while ViewSection's own fields are lowercase-tagged.
// encoding/json matches case-insensitively, so a single decoder handles both.
func TestFromMap_CapitalizedPrimitiveKeys(t *testing.T) {
	m := map[string]any{
		"title": "Wire",
		"sections": []any{
			map[string]any{
				"kind":    "metrics",
				"metrics": []any{map[string]any{"Label": "Total", "Value": "9", "Tone": "ok"}},
			},
			map[string]any{
				"kind":    "table",
				"headers": []any{"H"},
				"rows":    []any{[]any{map[string]any{"Text": "cell"}}},
			},
			map[string]any{
				"kind": "bars",
				"bars": []any{map[string]any{"Label": "P", "Done": float64(2), "Total": float64(5)}},
			},
		},
	}
	v, err := FromMap(m)
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	out := v.ProjectText()
	for _, w := range []string{"Total:", "9", "cell", "P:", "2/5"} {
		if !strings.Contains(out, w) {
			t.Errorf("decoded projection missing %q\n%s", w, out)
		}
	}
}

func TestExtract_NestedAndTopLevelAndAbsent(t *testing.T) {
	nested := map[string]any{"viewSpec": map[string]any{"title": "N", "sections": []any{}}}
	if v, ok := Extract(nested); !ok || v.Title != "N" {
		t.Errorf("nested viewSpec not extracted: %v %v", v, ok)
	}

	top := map[string]any{"title": "T", "sections": []any{
		map[string]any{"kind": "list", "items": []any{"x"}},
	}}
	if v, ok := Extract(top); !ok || v.Title != "T" {
		t.Errorf("top-level viewSpec not extracted: %v %v", v, ok)
	}

	// No spec present -> graceful (false), so the caller falls back to its own rendering.
	if _, ok := Extract(map[string]any{"status": "ok"}); ok {
		t.Error("Extract must return false when no spec present")
	}
	if _, ok := Extract(nil); ok {
		t.Error("Extract(nil) must return false")
	}
	// Empty spec must not suppress fallback.
	if _, ok := Extract(map[string]any{"viewSpec": map[string]any{}}); ok {
		t.Error("empty viewSpec must return false so caller falls back")
	}
}
