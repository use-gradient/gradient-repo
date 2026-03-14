package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func NewIntegrationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "integration",
		Aliases: []string{"intg"},
		Short:   "Manage integrations (Linear, Claude Code)",
		Long:    "Connect and configure third-party services for agent tasks.",
	}

	cmd.AddCommand(newIntegrationStatusCmd())
	cmd.AddCommand(newIntegrationClaudeCmd())
	cmd.AddCommand(newIntegrationLinearCmd())

	return cmd
}

func newIntegrationStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show integration status for current org",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var status map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/integrations/status", nil, &status); err != nil {
				return fmt.Errorf("failed to get status: %w", err)
			}

			fmt.Println("Integration Status")
			fmt.Println("──────────────────")

			linear, _ := status["linear"].(map[string]interface{})
			if connected, _ := linear["connected"].(bool); connected {
				fmt.Printf("  ✓ Linear:  Connected (%s)\n", str(linear["workspace_name"]))
			} else {
				fmt.Println("  ○ Linear:  Not connected")
			}

			claude, _ := status["claude"].(map[string]interface{})
			if configured, _ := claude["configured"].(bool); configured {
				fmt.Printf("  ✓ Claude:  Configured (model: %s)\n", str(claude["model"]))
			} else {
				fmt.Println("  ○ Claude:  Not configured")
			}

			billing, _ := status["billing"].(map[string]interface{})
			if active, _ := billing["active"].(bool); active {
				fmt.Printf("  ✓ Billing: Active (%s)\n", str(billing["tier"]))
			} else {
				fmt.Println("  ○ Billing: Not set up")
			}

			repos, _ := status["repos"].(map[string]interface{})
			if connected, _ := repos["connected"].(bool); connected {
				fmt.Printf("  ✓ Repos:   %v connected\n", repos["count"])
			} else {
				fmt.Println("  ○ Repos:   None connected")
			}

			if ready, _ := status["ready"].(bool); ready {
				fmt.Println("\n🚀 Agent tasks are ready!")
			} else {
				fmt.Println("\n⚠️  Complete setup to enable agent tasks")
				fmt.Println("   Run: gc integration claude --api-key <key>")
			}

			return nil
		},
	}
	return cmd
}

func newIntegrationClaudeCmd() *cobra.Command {
	var (
		apiKey      string
		model       string
		maxTurns    int
		enableTeams bool
		remove      bool
	)

	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Configure Claude Code (Anthropic API key)",
		Example: `  gc integration claude --api-key sk-ant-...
  gc integration claude --api-key sk-ant-... --model claude-sonnet-4-20250514
  gc integration claude --remove`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if remove {
				if err := client.DoJSON("DELETE", "/api/v1/integrations/claude", nil, nil); err != nil {
					return fmt.Errorf("failed to remove Claude config: %w", err)
				}
				fmt.Println("✓ Claude Code configuration removed")
				return nil
			}

			if apiKey == "" {
				// Show current config
				var cfg map[string]interface{}
				if err := client.DoJSON("GET", "/api/v1/integrations/claude", nil, &cfg); err != nil {
					return fmt.Errorf("failed to get config: %w", err)
				}
				data, _ := json.MarshalIndent(cfg, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			body := map[string]interface{}{
				"api_key":      apiKey,
				"model":        model,
				"max_turns":    maxTurns,
				"enable_teams": enableTeams,
			}

			var result map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/integrations/claude", body, &result); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Println("✓ Claude Code configured")
			fmt.Printf("  Model:    %s\n", str(result["model"]))
			fmt.Printf("  API Key:  %s\n", str(result["api_key_masked"]))
			return nil
		},
	}

	cmd.Flags().StringVar(&apiKey, "api-key", "", "Anthropic API key (sk-ant-...)")
	cmd.Flags().StringVar(&model, "model", "claude-sonnet-4-20250514", "Claude model")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 250, "Max conversation turns per chunk (auto-resumes up to 2x)")
	cmd.Flags().BoolVar(&enableTeams, "enable-teams", true, "Enable agent teams for complex tasks")
	cmd.Flags().BoolVar(&remove, "remove", false, "Remove Claude configuration")

	return cmd
}

func newIntegrationLinearCmd() *cobra.Command {
	var remove bool

	cmd := &cobra.Command{
		Use:   "linear",
		Short: "Manage Linear integration",
		Long:  "View or disconnect the Linear workspace connection. Connect via the dashboard: /dashboard/integrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if remove {
				if err := client.DoJSON("DELETE", "/api/v1/integrations/linear", nil, nil); err != nil {
					return fmt.Errorf("failed to disconnect Linear: %w", err)
				}
				fmt.Println("✓ Linear disconnected")
				return nil
			}

			var conn map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/integrations/linear", nil, &conn); err != nil {
				return fmt.Errorf("failed to get Linear connection: %w", err)
			}

			if connected, _ := conn["connected"].(bool); connected {
				fmt.Println("✓ Linear connected")
				fmt.Printf("  Workspace: %s\n", str(conn["workspace_name"]))
				fmt.Printf("  Trigger:   Issues in '%s' state with label matching filter\n", str(conn["trigger_state"]))
			} else {
				fmt.Println("○ Linear not connected")
				fmt.Println("  Connect via the dashboard: https://usegradient.dev/dashboard/integrations")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&remove, "remove", false, "Disconnect Linear")

	return cmd
}
