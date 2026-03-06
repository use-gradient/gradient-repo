package commands

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func NewOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations",
	}

	cmd.AddCommand(NewOrgListCmd())
	cmd.AddCommand(NewOrgCreateCmd())
	cmd.AddCommand(NewOrgSwitchCmd())
	cmd.AddCommand(NewOrgCurrentCmd())
	cmd.AddCommand(NewOrgMembersCmd())
	cmd.AddCommand(NewOrgInviteCmd())
	cmd.AddCommand(NewOrgRemoveCmd())
	cmd.AddCommand(NewOrgInvitationsCmd())
	cmd.AddCommand(NewOrgRegistryCmd())

	return cmd
}

func NewOrgListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List your organizations",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var orgs []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/orgs", nil, &orgs); err != nil {
				return err
			}

			cfg := LoadCLIConfig()

			if len(orgs) == 0 {
				fmt.Println("No organizations found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSLUG\tACTIVE")
			for _, org := range orgs {
				active := ""
				if fmt.Sprint(org["id"]) == cfg.ActiveOrg {
					active = "✓"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					org["id"], org["name"], org["slug"], active)
			}
			w.Flush()

			return nil
		},
	}
	return cmd
}

func NewOrgCreateCmd() *cobra.Command {
	var slug string
	var switchTo bool

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new organization",
		Long: `Create a new organization in Clerk. You will be added as the admin.

Examples:
  gc org create "My Team"
  gc org create "Acme Corp" --slug acme --switch`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{
				"name": args[0],
			}
			if slug != "" {
				body["slug"] = slug
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/orgs", body, &result); err != nil {
				return err
			}

			orgID := fmt.Sprint(result["id"])
			orgName := fmt.Sprint(result["name"])
			orgSlug := fmt.Sprint(result["slug"])

			fmt.Println()
			fmt.Println("  ✓ Organization created!")
			fmt.Println()
			fmt.Printf("  Name:  %s\n", orgName)
			fmt.Printf("  ID:    %s\n", orgID)
			if orgSlug != "" {
				fmt.Printf("  Slug:  %s\n", orgSlug)
			}

			if switchTo {
				cfg := LoadCLIConfig()
				cfg.ActiveOrg = orgID
				if err := SaveCLIConfig(cfg); err != nil {
					return fmt.Errorf("org created but failed to switch: %w", err)
				}
				fmt.Println()
				fmt.Printf("  ✓ Switched to %s\n", orgName)
			} else {
				fmt.Println()
				fmt.Printf("  Switch to it: gc org switch %s\n", orgID)
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&slug, "slug", "", "URL-friendly slug (auto-generated if not set)")
	cmd.Flags().BoolVar(&switchTo, "switch", true, "Automatically switch to the new org")

	return cmd
}

func NewOrgSwitchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switch [org-id]",
		Short: "Switch active organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadCLIConfig()
			cfg.ActiveOrg = args[0]

			if err := SaveCLIConfig(cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("✓ Switched to organization: %s\n", args[0])
			fmt.Println("  All subsequent commands will use this org for billing and access.")
			return nil
		},
	}
	return cmd
}

func NewOrgCurrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show current active organization",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadCLIConfig()

			if cfg.ActiveOrg == "" {
				fmt.Println("No active organization set.")
				fmt.Println("Use 'gc org switch <org-id>' to set one.")
			} else {
				fmt.Printf("Active org: %s\n", cfg.ActiveOrg)
			}

			return nil
		},
	}
	return cmd
}

func NewOrgMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "members",
		Short:   "List members of the current organization",
		Aliases: []string{"who"},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var members []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/orgs/members", nil, &members); err != nil {
				return err
			}

			if len(members) == 0 {
				fmt.Println("No members found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "USER ID\tEMAIL\tNAME\tROLE")
			for _, m := range members {
				name := ""
				if fn, ok := m["first_name"].(string); ok && fn != "" {
					name = fn
				}
				if ln, ok := m["last_name"].(string); ok && ln != "" {
					if name != "" {
						name += " "
					}
					name += ln
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					m["user_id"], m["email"], name, m["role"])
			}
			w.Flush()

			return nil
		},
	}
	return cmd
}

func NewOrgInviteCmd() *cobra.Command {
	var role string

	cmd := &cobra.Command{
		Use:   "invite [email]",
		Short: "Invite a member to the current organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{
				"email": args[0],
				"role":  role,
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/orgs/invite", body, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Invitation sent to %s\n", args[0])
			if id, ok := result["id"]; ok {
				fmt.Printf("  Invitation ID: %s\n", id)
			}
			if status, ok := result["status"]; ok {
				fmt.Printf("  Status: %s\n", status)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&role, "role", "org:member", "Role for the invited member (org:member, org:admin)")

	return cmd
}

func NewOrgRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove [user-id]",
		Short: "Remove a member from the current organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("DELETE", "/api/v1/orgs/members/"+args[0], nil, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Member %s removed from organization\n", args[0])

			return nil
		},
	}
	return cmd
}

func NewOrgInvitationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invitations",
		Short:   "List pending invitations for the current organization",
		Aliases: []string{"invites"},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var invitations []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/orgs/invitations", nil, &invitations); err != nil {
				return err
			}

			if len(invitations) == 0 {
				fmt.Println("No pending invitations.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tEMAIL\tROLE\tSTATUS")
			for _, inv := range invitations {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					inv["id"], inv["email_address"], inv["role"], inv["status"])
			}
			w.Flush()

			return nil
		},
	}

	cmd.AddCommand(NewOrgRevokeInviteCmd())

	return cmd
}

func NewOrgRevokeInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke [invitation-id]",
		Short: "Revoke a pending invitation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/orgs/invitations/"+args[0]+"/revoke", nil, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Invitation %s revoked\n", args[0])

			return nil
		},
	}
	return cmd
}

// NewOrgRegistryCmd manages the org's custom container registry.
func NewOrgRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage org container registry (enterprise snapshot isolation)",
		Long: `Configure a custom container registry for your organization.

By default, all orgs use the platform's shared registry. Enterprise orgs
can set their own registry so snapshots stay in their infrastructure.

Examples:
  gc org registry get                                    # Show current registry
  gc org registry set --url ghcr.io/myco/gradient-envs   # Set custom registry
  gc org registry clear                                  # Revert to platform default`,
	}

	cmd.AddCommand(newOrgRegistryGetCmd())
	cmd.AddCommand(newOrgRegistrySetCmd())
	cmd.AddCommand(newOrgRegistryClearCmd())

	return cmd
}

func newOrgRegistryGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show current registry configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/orgs/settings/registry", nil, &result); err != nil {
				return err
			}

			isDefault, _ := result["using_default"].(bool)
			if isDefault {
				fmt.Println("Registry: platform default (shared)")
				fmt.Println("  → Set a custom registry with: gc org registry set --url <registry-url>")
			} else {
				fmt.Printf("Registry: %s\n", result["registry_url"])
				fmt.Printf("Username: %s\n", result["registry_user"])
				fmt.Println("  → Clear with: gc org registry clear")
			}
			return nil
		},
	}
}

func newOrgRegistrySetCmd() *cobra.Command {
	var url, user, pass string

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a custom container registry for this org",
		Long: `Set a custom container registry for snapshot isolation.

All new environments and pre-destroy snapshots will push to this registry.
Existing environments keep using whatever registry they were booted with.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				return fmt.Errorf("--url is required")
			}
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{
				"registry_url":  url,
				"registry_user": user,
				"registry_pass": pass,
			}

			var result map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/orgs/settings/registry", body, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Registry set to: %s\n", url)
			fmt.Println("  All new environments will use this registry for snapshots.")
			return nil
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Registry URL (e.g. ghcr.io/myco/gradient-envs)")
	cmd.Flags().StringVar(&user, "user", "", "Registry username")
	cmd.Flags().StringVar(&pass, "pass", "", "Registry password/token")
	_ = cmd.MarkFlagRequired("url")

	return cmd
}

func newOrgRegistryClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Revert to platform default registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("DELETE", "/api/v1/orgs/settings/registry", nil, &result); err != nil {
				return err
			}

			fmt.Println("✓ Reverted to platform default registry.")
			return nil
		},
	}
}
