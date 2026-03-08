package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects stdout and captures output during fn execution.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestRenderComparisonTableEmptyData(t *testing.T) {
	data := map[string]interface{}{}
	err := renderComparisonTable(data)
	if err != nil {
		t.Fatalf("renderComparisonTable should not error on empty data: %v", err)
	}
}

func TestRenderComparisonTableSingleProvider(t *testing.T) {
	data := map[string]interface{}{
		"virtual-machine": []interface{}{
			map[string]interface{}{
				"headers": []interface{}{
					"Europe",
					map[string]interface{}{"name": "aws"},
				},
				"rows": []interface{}{
					[]interface{}{
						// region object
						map[string]interface{}{"name": "Frankfurt", "country": "DE"},
						// provider cell: component → priceObj
						map[string]interface{}{
							"web-server": map[string]interface{}{
								"pricePerHour": 0.0456,
								"virtualMachine": map[string]interface{}{
									"name": "t3.medium",
									"cpu":  "2",
									"ram":  "4",
								},
							},
						},
					},
				},
				"total": float64(5),
			},
		},
	}

	output := captureStdout(func() {
		renderComparisonTable(data)
	})

	// Should contain the table ID
	if !strings.Contains(output, "virtual-machine") {
		t.Error("Output should contain table ID 'virtual-machine'")
	}
	// Should contain region name
	if !strings.Contains(output, "Frankfurt") {
		t.Error("Output should contain region name 'Frankfurt'")
	}
	// Should contain VM name
	if !strings.Contains(output, "t3.medium") {
		t.Error("Output should contain VM name 't3.medium'")
	}
	// Should contain monthly price
	if !strings.Contains(output, "$33.") {
		t.Errorf("Output should contain monthly price (~$33.29), got: %s", output)
	}
	// Should contain TOTAL
	if !strings.Contains(output, "TOTAL") {
		t.Error("Output should contain TOTAL row")
	}
}

func TestRenderComparisonTableMultipleProviders(t *testing.T) {
	data := map[string]interface{}{
		"virtual-machine": []interface{}{
			map[string]interface{}{
				"headers": []interface{}{
					"Europe",
					map[string]interface{}{"name": "aws"},
					map[string]interface{}{"name": "gcp"},
				},
				"rows": []interface{}{
					[]interface{}{
						map[string]interface{}{"name": "Frankfurt", "country": "DE"},
						map[string]interface{}{
							"web-server": map[string]interface{}{
								"pricePerHour": 0.0456,
								"virtualMachine": map[string]interface{}{
									"name": "t3.medium",
								},
							},
						},
						map[string]interface{}{
							"web-server": map[string]interface{}{
								"pricePerHour": 0.0332,
								"virtualMachine": map[string]interface{}{
									"name": "e2-medium",
								},
							},
						},
					},
				},
				"total": float64(10),
			},
		},
	}

	output := captureStdout(func() {
		renderComparisonTable(data)
	})

	if !strings.Contains(output, "AWS") {
		t.Error("Output should contain provider 'AWS'")
	}
	if !strings.Contains(output, "GCP") {
		t.Error("Output should contain provider 'GCP'")
	}
	if !strings.Contains(output, "t3.medium") {
		t.Error("Output should contain 't3.medium'")
	}
	if !strings.Contains(output, "e2-medium") {
		t.Error("Output should contain 'e2-medium'")
	}
}

func TestRenderComparisonTableNotAvailable(t *testing.T) {
	data := map[string]interface{}{
		"virtual-machine": []interface{}{
			map[string]interface{}{
				"headers": []interface{}{
					"Europe",
					map[string]interface{}{"name": "aws"},
				},
				"rows": []interface{}{
					[]interface{}{
						map[string]interface{}{"name": "TestRegion"},
						map[string]interface{}{
							"web-server": map[string]interface{}{
								"notAvailable": true,
							},
						},
					},
				},
				"total": float64(1),
			},
		},
	}

	output := captureStdout(func() {
		renderComparisonTable(data)
	})

	if !strings.Contains(output, "N/A") {
		t.Error("Output should show 'N/A' for not-available components")
	}
}

func TestExtractProviderNameFromCSP(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected string
	}{
		{map[string]interface{}{"name": "aws"}, "aws"},
		{map[string]interface{}{"csp": map[string]interface{}{"name": "azure"}}, "azure"},
		{"raw-string", "raw-string"},
	}

	for _, tt := range tests {
		got := extractProviderName(tt.input)
		if got != tt.expected {
			t.Errorf("extractProviderName(%v): got %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractRegionName(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected string
	}{
		{map[string]interface{}{"name": "Frankfurt", "country": "DE"}, "Frankfurt (DE)"},
		{map[string]interface{}{"name": "London"}, "London"},
		{map[string]interface{}{}, "-"},
	}

	for _, tt := range tests {
		got := extractRegionName(tt.input)
		if got != tt.expected {
			t.Errorf("extractRegionName(%v): got %q, want %q", tt.input, got, tt.expected)
		}
	}
}
