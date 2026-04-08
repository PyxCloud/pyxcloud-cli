package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"github.com/spf13/cobra"
)

const (
	flagUserID = "user-id"
	flagRole   = "role"
)

// settingsCmd mirrors the "Settings" sidebar page.
var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Organisation settings, team, roles, and API tokens",
	Long: `Settings commands mirror the Settings page in the PyxCloud console.

  pyxcloud settings whoami
  pyxcloud settings team
  pyxcloud settings seats
  pyxcloud settings invite --email user@example.com
  pyxcloud settings assign-role --user-id <id> --role pyx-developer-role
  pyxcloud settings tokens
  pyxcloud settings create-token --name "ci-pipeline"
  pyxcloud settings revoke-token --id 42`,
}

// ── whoami ───────────────────────────────────────────────────────────────

var settingsWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current user identity and roles",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		me, err := client.SettingsMe()
		if err != nil {
			return fmt.Errorf("whoami: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "  Name:\t%v\n", me["name"])
		fmt.Fprintf(w, "  Email:\t%v\n", me["email"])
		fmt.Fprintf(w, "  Admin:\t%v\n", me["isAdmin"])
		if roles, ok := me["roles"].([]interface{}); ok {
			roleStrs := make([]string, len(roles))
			for i, r := range roles {
				roleStrs[i] = fmt.Sprintf("%v", r)
			}
			fmt.Fprintf(w, "  Roles:\t%s\n", strings.Join(roleStrs, ", "))
		}
		if org, ok := me["organisation"].(string); ok && org != "" {
			fmt.Fprintf(w, "  Organisation:\t%s\n", org)
		}
		fmt.Fprintln(w, "")
		return w.Flush()
	},
}

// ── team ─────────────────────────────────────────────────────────────────

var settingsTeamCmd = &cobra.Command{
	Use:   "team",
	Short: "List organisation team members (admin only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		users, err := client.SettingsUsers()
		if err != nil {
			return fmt.Errorf("list team: %w", err)
		}

		if len(users) == 0 {
			fmt.Println("No team members found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tEMAIL\tROLES")
		fmt.Fprintln(w, "--\t----\t-----\t-----")
		for _, u := range users {
			roles := "-"
			if r, ok := u["roles"].([]interface{}); ok && len(r) > 0 {
				parts := make([]string, len(r))
				for i, role := range r {
					parts[i] = fmt.Sprintf("%v", role)
				}
				roles = strings.Join(parts, ", ")
			}
			fmt.Fprintf(w, "%v\t%v\t%v\t%s\n",
				u["id"], u["name"], u["email"], roles)
		}
		return w.Flush()
	},
}

// ── seats ────────────────────────────────────────────────────────────────

var settingsSeatsCmd = &cobra.Command{
	Use:   "seats",
	Short: "Show seat usage for the organisation",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		seats, err := client.SettingsSeats()
		if err != nil {
			return fmt.Errorf("seats: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "  Used:\t%v\n", seats["used"])
		fmt.Fprintf(w, "  Max:\t%v\n", seats["max"])
		fmt.Fprintf(w, "  Remaining:\t%v\n", seats["remaining"])
		fmt.Fprintln(w, "")
		return w.Flush()
	},
}

// ── invite ───────────────────────────────────────────────────────────────

var settingsInviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Invite a user to the organisation",
	RunE: func(cmd *cobra.Command, args []string) error {
		email, _ := cmd.Flags().GetString("email")
		if email == "" {
			return fmt.Errorf("--email is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{"email": email}
		_, err = client.SettingsInvite(body)
		if err != nil {
			return fmt.Errorf("invite: %w", err)
		}

		fmt.Printf("Invitation sent to %s.\n", email)
		return nil
	},
}

// ── assign-role ──────────────────────────────────────────────────────────

var settingsAssignRoleCmd = &cobra.Command{
	Use:   "assign-role",
	Short: "Assign a role to a user",
	RunE: func(cmd *cobra.Command, args []string) error {
		userID, _ := cmd.Flags().GetString(flagUserID)
		role, _ := cmd.Flags().GetString(flagRole)
		if userID == "" || role == "" {
			return fmt.Errorf("--user-id and --role are required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{"userId": userID, "roleName": role}
		_, err = client.SettingsAssignRole(body)
		if err != nil {
			return fmt.Errorf("assign role: %w", err)
		}

		fmt.Printf("Role %s assigned to user %s.\n", role, userID)
		return nil
	},
}

// ── remove-role ──────────────────────────────────────────────────────────

var settingsRemoveRoleCmd = &cobra.Command{
	Use:   "remove-role",
	Short: "Remove a role from a user",
	RunE: func(cmd *cobra.Command, args []string) error {
		userID, _ := cmd.Flags().GetString(flagUserID)
		role, _ := cmd.Flags().GetString(flagRole)
		if userID == "" || role == "" {
			return fmt.Errorf("--user-id and --role are required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{"userId": userID, "roleName": role}
		_, err = client.SettingsRemoveRole(body)
		if err != nil {
			return fmt.Errorf("remove role: %w", err)
		}

		fmt.Printf("Role %s removed from user %s.\n", role, userID)
		return nil
	},
}

// ── tokens ───────────────────────────────────────────────────────────────

var settingsTokensCmd = &cobra.Command{
	Use:   "tokens",
	Short: "List CLI API tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		tokens, err := client.TokenList()
		if err != nil {
			return fmt.Errorf("list tokens: %w", err)
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tCREATED\tLAST USED")
		fmt.Fprintln(w, "--\t----\t-------\t---------")
		for _, t := range tokens {
			lastUsed := "-"
			if lu, ok := t["lastUsedAt"].(string); ok && lu != "" {
				lastUsed = lu
			}
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
				t["id"], t["name"], t["createdAt"], lastUsed)
		}
		return w.Flush()
	},
}

var settingsCreateTokenCmd = &cobra.Command{
	Use:   "create-token",
	Short: "Create a new CLI API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			return fmt.Errorf("--name is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		body := map[string]string{"name": name}
		result, err := client.TokenCreate(body)
		if err != nil {
			return fmt.Errorf("create token: %w", err)
		}

		fmt.Println("API Token created.")
		fmt.Printf("  Name:  %v\n", result["name"])
		if token, ok := result["token"].(string); ok {
			fmt.Printf("  Token: %s\n", token)
			fmt.Println("\nSave this token now -- it will not be shown again.")
		}
		return nil
	},
}

var settingsRevokeTokenCmd = &cobra.Command{
	Use:   "revoke-token",
	Short: "Revoke a CLI API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		tokenID, _ := cmd.Flags().GetString("id")
		if tokenID == "" {
			return fmt.Errorf("--id is required")
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		if err := client.TokenRevoke(tokenID); err != nil {
			return fmt.Errorf("revoke token: %w", err)
		}

		fmt.Printf("Token %s revoked.\n", tokenID)
		return nil
	},
}

func init() {
	settingsInviteCmd.Flags().String("email", "", "Email of the user to invite (required)")
	settingsAssignRoleCmd.Flags().String(flagUserID, "", "User ID (required)")
	settingsAssignRoleCmd.Flags().String(flagRole, "", "Role name to assign (required)")
	settingsRemoveRoleCmd.Flags().String(flagUserID, "", "User ID (required)")
	settingsRemoveRoleCmd.Flags().String(flagRole, "", "Role name to remove (required)")
	settingsCreateTokenCmd.Flags().String("name", "", "Token name (required)")
	settingsRevokeTokenCmd.Flags().String("id", "", "Token ID to revoke (required)")

	settingsCmd.AddCommand(settingsWhoamiCmd)
	settingsCmd.AddCommand(settingsTeamCmd)
	settingsCmd.AddCommand(settingsSeatsCmd)
	settingsCmd.AddCommand(settingsInviteCmd)
	settingsCmd.AddCommand(settingsAssignRoleCmd)
	settingsCmd.AddCommand(settingsRemoveRoleCmd)
	settingsCmd.AddCommand(settingsTokensCmd)
	settingsCmd.AddCommand(settingsCreateTokenCmd)
	settingsCmd.AddCommand(settingsRevokeTokenCmd)
	rootCmd.AddCommand(settingsCmd)
}
