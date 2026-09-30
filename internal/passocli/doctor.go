package passocli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/spf13/cobra"
)

type doctorCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type doctorChecks struct {
	Profile    doctorCheck `json:"profile"`
	Ledger     doctorCheck `json:"ledger"`
	Skill      doctorCheck `json:"skill"`
	Executable doctorCheck `json:"executable"`
}
type doctorProfile struct {
	Name      string `json:"name"`
	APIURL    string `json:"apiUrl,omitempty"`
	IssuerURL string `json:"issuerUrl,omitempty"`
}
type doctorReport struct {
	SchemaVersion int           `json:"schemaVersion"`
	Status        string        `json:"status"`
	Profile       doctorProfile `json:"profile"`
	BuildVersion  string        `json:"buildVersion"`
	Checks        doctorChecks  `json:"checks"`
	Error         string        `json:"error,omitempty"`
}

func newDoctorCommand(profile, ledgerPath *string, project *int64, asJSON *bool, homeDir func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local passo CLI configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, code := runDoctor(*profile, *ledgerPath, *project, homeDir)
			if *asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(report); err != nil {
					return err
				}
			} else {
				if err := writeDoctorHuman(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			}
			if code != "" {
				return &ExitError{20, code}
			}
			return nil
		},
	}
}

func runDoctor(profile, ledgerPath string, project int64, homeDir func() (string, error)) (doctorReport, string) {
	r := doctorReport{SchemaVersion: 1, Status: "ok", Profile: doctorProfile{Name: profile}, BuildVersion: doctorBuildVersion()}
	code := ""
	p, err := passoauth.ResolveProfile(profile)
	if err != nil {
		r.Checks.Profile = doctorCheck{"error", "profile is invalid or incomplete"}
		code = "invalid_profile"
	} else {
		r.Profile.APIURL, r.Profile.IssuerURL = p.APIURL, p.IssuerURL
		r.Checks.Profile = doctorCheck{"ok", "profile endpoints are configured and pass safety checks"}
	}
	if project < 0 {
		r.Checks.Ledger = doctorCheck{"error", "ledger scope does not match selected profile or project"}
		if code == "" {
			code = "scope_mismatch"
		}
	} else {
		ledger, ledgerErr := passostate.Load(ledgerPath)
		if ledgerErr != nil {
			r.Checks.Ledger = doctorCheck{"error", "ledger is unreadable or invalid"}
			if code == "" {
				code = "invalid_ledger"
			}
		} else if _, statErr := os.Stat(ledgerPath); os.IsNotExist(statErr) {
			r.Checks.Ledger = doctorCheck{"warning", "ledger is not present yet"}
		} else if statErr != nil {
			r.Checks.Ledger = doctorCheck{"error", "ledger is unreadable or invalid"}
			if code == "" {
				code = "invalid_ledger"
			}
		} else if ledger.Profile != "" && ledger.Profile != profile || ledger.ProjectID > 0 && project > 0 && ledger.ProjectID != project {
			r.Checks.Ledger = doctorCheck{"error", "ledger scope does not match selected profile or project"}
			if code == "" {
				code = "scope_mismatch"
			}
		} else {
			r.Checks.Ledger = doctorCheck{"ok", "ledger is readable and scope is valid"}
		}
	}
	home, homeErr := homeDir()
	if homeErr != nil || home == "" {
		r.Checks.Skill = doctorCheck{"absent", "shared agent skill is absent"}
	} else if _, action, skillErr := planPassoSkill(home, false); skillErr != nil {
		r.Checks.Skill = doctorCheck{"unmanaged", "shared agent skill marker is missing or invalid"}
	} else if action == "refresh" {
		r.Checks.Skill = doctorCheck{"installed", "managed shared agent skill is installed"}
	} else {
		r.Checks.Skill = doctorCheck{"absent", "shared agent skill is absent"}
	}
	if _, err := os.Executable(); err != nil {
		r.Checks.Executable = doctorCheck{"warning", "executable path is unavailable"}
	} else {
		r.Checks.Executable = doctorCheck{"ok", "CLI executable is available"}
	}
	if code != "" {
		r.Status, r.Error = "error", code
	} else if r.Checks.Ledger.Status == "warning" || r.Checks.Skill.Status == "absent" || r.Checks.Skill.Status == "unmanaged" || r.Checks.Executable.Status == "warning" {
		r.Status = "warning"
	}
	return r, code
}

func doctorBuildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func writeDoctorHuman(w io.Writer, report doctorReport) error {
	if _, err := fmt.Fprintf(w, "passo doctor: %s\n", report.Status); err != nil {
		return err
	}
	for _, check := range []struct {
		name  string
		check doctorCheck
	}{
		{"profile", report.Checks.Profile}, {"ledger", report.Checks.Ledger}, {"skill", report.Checks.Skill}, {"executable", report.Checks.Executable},
	} {
		if _, err := fmt.Fprintf(w, "  %s: %s — %s\n", check.name, check.check.Status, check.check.Detail); err != nil {
			return err
		}
	}
	return nil
}
