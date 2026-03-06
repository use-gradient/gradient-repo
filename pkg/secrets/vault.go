package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VaultClient provides read/write access to HashiCorp Vault secrets.
// Uses the HTTP API directly to avoid the heavy Vault SDK dependency.
type VaultClient struct {
	addr   string // Vault address (e.g. http://127.0.0.1:8200)
	token  string // Vault token for authentication
	client *http.Client
}

// NewVaultClient creates a new Vault client.
func NewVaultClient(addr, token string) (*VaultClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("vault address is required")
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}

	// Normalize address
	addr = strings.TrimRight(addr, "/")

	return &VaultClient{
		addr:  addr,
		token: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// ReadSecret reads a secret from Vault KV v2 engine.
// path should be like "secret/data/my-app/db-password"
func (v *VaultClient) ReadSecret(ctx context.Context, path string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/v1/%s", v.addr, path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read vault response: %w", err)
	}

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			Data map[string]interface{} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode vault response: %w", err)
	}

	return result.Data.Data, nil
}

// WriteSecret writes a secret to Vault KV v2 engine.
// path should be like "secret/data/my-app/db-password"
func (v *VaultClient) WriteSecret(ctx context.Context, path string, data map[string]interface{}) error {
	payload := map[string]interface{}{
		"data": data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal secret data: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s", v.addr, path)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DeleteSecret deletes a secret from Vault KV v2 engine.
func (v *VaultClient) DeleteSecret(ctx context.Context, path string) error {
	url := fmt.Sprintf("%s/v1/%s", v.addr, path)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Health checks if Vault is reachable and unsealed.
func (v *VaultClient) Health(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/sys/health", v.addr)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault health check failed: %w", err)
	}
	defer resp.Body.Close()

	// Vault returns 200 for initialized+unsealed, 429 for standby, 501 for not init, 503 for sealed
	if resp.StatusCode != 200 && resp.StatusCode != 429 {
		return fmt.Errorf("vault is not healthy (status %d)", resp.StatusCode)
	}

	return nil
}

// SecretSyncer syncs secrets from Vault to a running environment.
type SecretSyncer struct {
	vault *VaultClient
}

// NewSecretSyncer creates a new syncer that reads from Vault and injects into containers.
func NewSecretSyncer(vault *VaultClient) *SecretSyncer {
	return &SecretSyncer{vault: vault}
}

// SyncToEnvironment reads a secret from Vault and returns the key-value pairs.
// The caller (API handler) is responsible for injecting these into the environment
// (e.g., via SSH + docker exec to set env vars).
func (s *SecretSyncer) SyncToEnvironment(ctx context.Context, vaultPath string) (map[string]interface{}, error) {
	if s.vault == nil {
		return nil, fmt.Errorf("vault client not configured — set VAULT_ADDR and VAULT_TOKEN")
	}

	secrets, err := s.vault.ReadSecret(ctx, vaultPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret from vault path %s: %w", vaultPath, err)
	}

	return secrets, nil
}

// ListSecrets lists secret keys at a given path (KV v2 metadata).
func (v *VaultClient) ListSecrets(ctx context.Context, path string) ([]string, error) {
	// KV v2 list endpoint uses metadata path
	url := fmt.Sprintf("%s/v1/%s", v.addr, path)

	req, err := http.NewRequestWithContext(ctx, "LIST", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return []string{}, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode vault response: %w", err)
	}

	return result.Data.Keys, nil
}
