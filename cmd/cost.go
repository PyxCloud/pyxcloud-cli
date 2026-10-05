package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/PyxCloud/terraform-provider-pyxcloud/pkg/billingscan"
	"github.com/PyxCloud/terraform-provider-pyxcloud/pkg/iacsecscan"
	"github.com/spf13/cobra"
)

var costJSON bool
var secScanJSON bool

var costCmd = &cobra.Command{
	Use:   "cost <plan-json>",
	Short: "Scan billing cost JSON {current,baseline} for blowout signals (CI gate)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := billingscan.ScanFile(args[0])
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		if !res.OK {
			os.Exit(1)
		}
		return nil
	},
}

var secScanCmd = &cobra.Command{
	Use:   "sec-scan <plan-json>",
	Short: "Scan IaC resources JSON {resources:[{type,attributes}]} for security misconfigurations (CI gate)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := iacsecscan.ScanFile(args[0])
		if err != nil {
			return err
		}
		if secScanJSON {
			return json.NewEncoder(os.Stdout).Encode(res)
		}
		for _, f := range res.Findings {
			fmt.Printf("[%s] %s.%s %s: %s\n  remediation: %s\n",
				f.Severity, f.ResourceType, f.ResourceName, f.RuleID, f.Description, f.Remediation)
		}
		fmt.Printf("findings: %d\n", len(res.Findings))
		if !res.OK {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(costCmd)
	costCmd.Flags().BoolVar(&costJSON, "json", false, "machine-readable JSON output")
	rootCmd.AddCommand(secScanCmd)
	secScanCmd.Flags().BoolVar(&secScanJSON, "json", false, "machine-readable JSON output")
}
