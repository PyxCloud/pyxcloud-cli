package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Show comparison table for a build",
	Long: `Displays the pricing comparison table — identical data to the
PyxCloud console UI. Shows per-provider, per-region pricing for
each component in the architecture.

Example:
  pyxcloud architecture compare -p 42 -v 0.1.0
  pyxcloud architecture compare -p 42 -v 0.1.0 --table load-balancer`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, _ := cmd.Flags().GetString("project")
		buildVersion, _ := cmd.Flags().GetString("version")
		if projectID == "" || buildVersion == "" {
			return fmt.Errorf("--project and --version are required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		tableId, _ := cmd.Flags().GetString("table")
		// tableId left empty → backend auto-detects from architecture root nodes

		data, err := client.Compare(projectID, buildVersion, tableId)
		if err != nil {
			return fmt.Errorf("compare: %w", err)
		}

		return renderComparisonTable(data)
	},
}

// renderComparisonTable renders the real ComparisonTableService V2 JSON format.
//
// Format:
//
//	{
//	  "tableId": [                         ← one entry per macro-region
//	    {
//	      "headers": ["Europe", {csp1}, {csp2}, ...],
//	      "rows": [
//	        [regionObj, {comp1: priceObj, comp2: priceObj}, ...],
//	        ...
//	      ],
//	      "total": N
//	    }
//	  ]
//	}
func renderComparisonTable(data map[string]interface{}) error {
	for tableId, rawSections := range data {
		sections, ok := rawSections.([]interface{})
		if !ok {
			continue
		}

		fmt.Printf("\n  ── %s ──\n", tableId)

		for _, rawSection := range sections {
			section, ok := rawSection.(map[string]interface{})
			if !ok {
				continue
			}

			renderSection(section)
		}
	}
	return nil
}

func renderSection(section map[string]interface{}) {
	// Parse headers
	rawHeaders, _ := section["headers"].([]interface{})
	if len(rawHeaders) == 0 {
		return
	}

	// First header is the macro-region name (string)
	macroRegion := fmt.Sprintf("%v", rawHeaders[0])

	// Remaining headers are CSP/AccountBinding objects
	providerNames := make([]string, 0, len(rawHeaders)-1)
	for _, h := range rawHeaders[1:] {
		providerNames = append(providerNames, extractProviderName(h))
	}

	// Parse rows
	rawRows, _ := section["rows"].([]interface{})
	total, _ := section["total"].(float64)

	fmt.Printf("\n  %s (showing %d of %d regions)\n\n", macroRegion, len(rawRows), int(total))

	if len(rawRows) == 0 {
		fmt.Println("  (no pricing data)")
		return
	}

	// Collect all component names from the first row's provider cells
	componentNames := collectComponentNames(rawRows)

	// Build dynamic header
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Header row: REGION | PROVIDER1 | PROVIDER2 | ...
	headerParts := []string{"  REGION"}
	for _, pName := range providerNames {
		headerParts = append(headerParts, strings.ToUpper(pName))
	}
	fmt.Fprintln(w, strings.Join(headerParts, "\t"))

	// Separator
	sepParts := []string{"  ------"}
	for range providerNames {
		sepParts = append(sepParts, "------")
	}
	fmt.Fprintln(w, strings.Join(sepParts, "\t"))

	// Data rows
	for _, rawRow := range rawRows {
		row, ok := rawRow.([]interface{})
		if !ok || len(row) == 0 {
			continue
		}

		// First element: region object
		regionName := extractRegionName(row[0])

		// For each component, print a sub-row
		for _, compName := range componentNames {
			parts := []string{fmt.Sprintf("  %s", regionName)}
			regionName = "" // only show region on first sub-row

			for i := 1; i < len(row); i++ {
				cell, _ := row[i].(map[string]interface{})
				parts = append(parts, formatCellComponent(cell, compName))
			}

			fmt.Fprintln(w, strings.Join(parts, "\t"))
		}

		// Total row per region
		{
			parts := []string{"  "}
			for i := 1; i < len(row); i++ {
				cell, _ := row[i].(map[string]interface{})
				parts = append(parts, formatCellTotal(cell))
			}
			fmt.Fprintln(w, strings.Join(parts, "\t"))
		}
	}

	w.Flush()
}

// collectComponentNames gathers all unique component names across all provider cells.
func collectComponentNames(rows []interface{}) []string {
	seen := map[string]bool{}
	var names []string

	for _, rawRow := range rows {
		row, ok := rawRow.([]interface{})
		if !ok {
			continue
		}
		// provider cells start at index 1
		for i := 1; i < len(row); i++ {
			cell, ok := row[i].(map[string]interface{})
			if !ok {
				continue
			}
			for key := range cell {
				if !seen[key] {
					seen[key] = true
					names = append(names, key)
				}
			}
		}
	}
	return names
}

// extractProviderName gets a display name from a header object (CSP or AccountBinding).
func extractProviderName(h interface{}) string {
	m, ok := h.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("%v", h)
	}
	// AccountBinding has "csp" → "name"
	if csp, ok := m["csp"].(map[string]interface{}); ok {
		if name, ok := csp["name"].(string); ok {
			return name
		}
	}
	// Direct CSP object
	if name, ok := m["name"].(string); ok {
		return name
	}
	return fmt.Sprintf("%v", h)
}

// extractRegionName gets the region display name from a region object.
func extractRegionName(r interface{}) string {
	m, ok := r.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("%v", r)
	}
	if name, ok := m["name"].(string); ok {
		country := ""
		if c, ok := m["country"].(string); ok {
			country = c
		}
		if country != "" {
			return fmt.Sprintf("%s (%s)", name, country)
		}
		return name
	}
	return "-"
}

// formatCellComponent formats a single component within a provider cell.
// Each cell is {componentName: {pricePerHour, virtualMachine: {name, cpu, ram}, ...}}
func formatCellComponent(cell map[string]interface{}, compName string) string {
	if cell == nil {
		return "-"
	}
	comp, ok := cell[compName]
	if !ok {
		return "-"
	}
	m, ok := comp.(map[string]interface{})
	if !ok {
		return "-"
	}

	// Check if not available
	if na, ok := m["notAvailable"].(bool); ok && na {
		return "N/A"
	}

	// Extract VM name + price
	vmName := ""
	if vm, ok := m["virtualMachine"].(map[string]interface{}); ok {
		vmName, _ = vm["name"].(string)
	}

	price := getFloat(m, "pricePerHour")
	monthly := price * 730 // ~hours/month

	if vmName != "" {
		return fmt.Sprintf("%s $%.2f/mo", vmName, monthly)
	}
	if price > 0 {
		return fmt.Sprintf("$%.2f/mo", monthly)
	}
	return "-"
}

// formatCellTotal sums all component prices in a provider cell.
func formatCellTotal(cell map[string]interface{}) string {
	if cell == nil {
		return "-"
	}
	total := 0.0
	for _, raw := range cell {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		total += getFloat(m, "pricePerHour")
	}
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("TOTAL: $%.2f/mo", total*730)
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f
		case json.Number:
			val, _ := f.Float64()
			return val
		}
	}
	return 0
}

func init() {
	compareCmd.Flags().StringP("project", "p", "", "Project ID (required)")
	compareCmd.Flags().StringP("version", "v", "", "Build version (required)")
	compareCmd.Flags().String("table", "", "Architecture place name to compare (auto-detected if empty)")
	architectureCmd.AddCommand(compareCmd)
}
