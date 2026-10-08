package passocli

import (
	"context"
	"encoding/json"
	"github.com/spf13/cobra"
	"strconv"
	"time"
)

func newScanBaselineCommand(factory runtimeFactory) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{Use: "scan-baseline", Short: "Run security scanners against the current server-pinned version", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the exact accepted run using canonical scan reads")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		r, err := factory(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		if r.VersionSequence <= 0 {
			return &ExitError{20, "version_sequence_required"}
		}
		timeout := r.timeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}
		accepted, err := r.Perform(ctx, "secure", "securityscan:startSecurityBaselineScan", params, nil, nil, false)
		if err != nil {
			return err
		}
		var receipt struct {
			RunID     string `json:"runId"`
			ProjectID int64  `json:"projectId"`
		}
		if json.Unmarshal(accepted.Data, &receipt) != nil || receipt.RunID == "" || receipt.ProjectID != r.ProjectID {
			return &ExitError{30, "invalid_scan_receipt"}
		}
		if !wait {
			return r.Emit(accepted)
		}
		params["versionId"] = strconv.FormatInt(r.VersionSequence, 10)
		interval := r.PollInterval
		if interval <= 0 {
			interval = time.Second
		}
		for {
			observed, err := r.Perform(ctx, "secure", "securityscan:getSecurityScanRun", params, nil, nil, false)
			if err != nil {
				return err
			}
			var scan struct {
				RunID     string `json:"runId"`
				ProjectID int64  `json:"projectId"`
				Version   int64  `json:"projectVersionId"`
				State     string `json:"state"`
			}
			if json.Unmarshal(observed.Data, &scan) != nil || scan.RunID != receipt.RunID || scan.ProjectID != r.ProjectID || scan.Version != r.VersionSequence {
				return &ExitError{30, "scan_identity_mismatch"}
			}
			switch scan.State {
			case "COMPLETED":
				observed.Status = "completed"
				return r.Emit(observed)
			case "FAILED":
				return &ExitError{30, "scan_failed"}
			case "QUEUED", "RUNNING", "RETRYING":
			default:
				return &ExitError{30, "invalid_scan_state"}
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return &ExitError{30, "scan_wait_timeout"}
			case <-timer.C:
			}
		}
	}
	return cmd
}
