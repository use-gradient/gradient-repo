package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets (thin orchestrator on Vault / AWS Secrets Manager)",
	}

	cmd.AddCommand(NewSecretSyncCmd())

	return cmd
}

func NewSecretSyncCmd() *cobra.Command {
	var (
		backend     string
		keys        string
		backendPath string
	)

	cmd := &cobra.Command{
		Use:   "sync [env-id]",
		Short: "Sync secrets from backend to environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			envID := args[0]
			keyList := strings.Split(keys, ",")

			for _, key := range keyList {
				key = strings.TrimSpace(key)
				if key == "" {
					continue
				}

				body := map[string]string{
					"environment_id": envID,
					"secret_key":    key,
					"backend":       backend,
					"backend_path":  backendPath,
				}

				var result map[string]interface{}
				if err := client.DoJSON("POST", "/api/v1/secrets/sync", body, &result); err != nil {
					return fmt.Errorf("failed to sync secret '%s': %w", key, err)
				}

				fmt.Printf("✓ Synced secret: %s (backend: %s)\n", key, backend)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&backend, "backend", "", "Secret backend: vault, aws (required)")
	cmd.Flags().StringVar(&keys, "keys", "", "Comma-separated secret keys (required)")
	cmd.Flags().StringVar(&backendPath, "path", "", "Backend path (e.g., secret/data/myapp)")
	cmd.MarkFlagRequired("backend")
	cmd.MarkFlagRequired("keys")

	return cmd
}
