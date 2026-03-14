package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

// ClaudeService manages Claude Code configurations and provides exec hooks.
type ClaudeService struct {
	db *db.DB
}

func NewClaudeService(database *db.DB) *ClaudeService {
	return &ClaudeService{db: database}
}

// ─── Config CRUD ────────────────────────────────────────────────────────

// SaveConfig creates or updates a Claude config for an org (optionally per-user)
func (s *ClaudeService) SaveConfig(ctx context.Context, orgID, userID, apiKey, model string, maxTurns int, allowedTools []string, enableTeams bool, maxCost float64) (*models.ClaudeConfig, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic API key is required")
	}

	cfg := &models.ClaudeConfig{
		ID:               uuid.New().String(),
		OrgID:            orgID,
		UserID:           userID,
		AnthropicAPIKey:  apiKey,
		Model:            model,
		MaxTurns:         maxTurns,
		AllowedTools:     allowedTools,
		EnableTeams:      enableTeams,
		MaxCostPerTask:   maxCost,
		MaxTokensPerTask: 100000,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-20250514"
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 250
	}
	if len(cfg.AllowedTools) == 0 {
		cfg.AllowedTools = []string{"Edit", "Write", "Bash", "Read"}
	}

	toolsJSON, _ := json.Marshal(cfg.AllowedTools)

	var userIDPtr *string
	if userID != "" {
		userIDPtr = &userID
	}

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO claude_configs (id, org_id, user_id, anthropic_api_key, model, max_turns,
			allowed_tools, enable_teams, max_cost_per_task, max_tokens_per_task, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (org_id, user_id) DO UPDATE SET
			anthropic_api_key=EXCLUDED.anthropic_api_key, model=EXCLUDED.model,
			max_turns=EXCLUDED.max_turns, allowed_tools=EXCLUDED.allowed_tools,
			enable_teams=EXCLUDED.enable_teams,
			max_cost_per_task=EXCLUDED.max_cost_per_task, updated_at=NOW()`,
		cfg.ID, cfg.OrgID, userIDPtr, cfg.AnthropicAPIKey, cfg.Model, cfg.MaxTurns,
		string(toolsJSON), cfg.EnableTeams, cfg.MaxCostPerTask, cfg.MaxTokensPerTask,
		cfg.CreatedAt, cfg.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save claude config: %w", err)
	}

	cfg.APIKeyMasked = models.MaskAPIKey(cfg.AnthropicAPIKey)
	return cfg, nil
}

// GetConfig returns the Claude config for an org (optionally checking user-specific first)
func (s *ClaudeService) GetConfig(ctx context.Context, orgID, userID string) (*models.ClaudeConfig, error) {
	cfg := &models.ClaudeConfig{}
	var toolsJSON string
	var nullUserID *string

	var query string
	var args []interface{}

	if userID != "" {
		query = `
			SELECT id, org_id, user_id, anthropic_api_key, COALESCE(model, 'claude-sonnet-4-20250514'),
				COALESCE(max_turns, 250), COALESCE(allowed_tools::text, '["Edit","Write","Bash","Read"]'),
				COALESCE(enable_teams, true),
				COALESCE(max_cost_per_task, 0), COALESCE(max_tokens_per_task, 100000),
				created_at, updated_at
			FROM claude_configs
			WHERE org_id = $1 AND (user_id = $2 OR user_id IS NULL)
			ORDER BY user_id DESC NULLS LAST LIMIT 1`
		args = []interface{}{orgID, userID}
	} else {
		query = `
			SELECT id, org_id, user_id, anthropic_api_key, COALESCE(model, 'claude-sonnet-4-20250514'),
				COALESCE(max_turns, 250), COALESCE(allowed_tools::text, '["Edit","Write","Bash","Read"]'),
				COALESCE(enable_teams, true),
				COALESCE(max_cost_per_task, 0), COALESCE(max_tokens_per_task, 100000),
				created_at, updated_at
			FROM claude_configs
			WHERE org_id = $1
			Order BY user_id NULLS LAST LIMIT 1`
		args = []interface{}{orgID}
	}

	err := s.db.Pool.QueryRow(ctx, query, args...).Scan(
		&cfg.ID, &cfg.OrgID, &nullUserID, &cfg.AnthropicAPIKey, &cfg.Model, &cfg.MaxTurns,
		&toolsJSON, &cfg.EnableTeams, &cfg.MaxCostPerTask, &cfg.MaxTokensPerTask, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if nullUserID != nil {
		cfg.UserID = *nullUserID
	}
	json.Unmarshal([]byte(toolsJSON), &cfg.AllowedTools)
	cfg.APIKeyMasked = models.MaskAPIKey(cfg.AnthropicAPIKey)

	return cfg, nil
}

// DeleteConfig deletes a Claude config
func (s *ClaudeService) DeleteConfig(ctx context.Context, orgID string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM claude_configs WHERE org_id = $1`, orgID)
	return err
}

// HasConfig returns true if the org has a Claude config
func (s *ClaudeService) HasConfig(ctx context.Context, orgID string) bool {
	cfg, err := s.GetConfig(ctx, orgID, "")
	return err == nil && cfg != nil
}

func (s *ClaudeService) Complete(ctx context.Context, orgID, prompt string, maxTokens int) (string, string, error) {
	cfg, err := s.GetConfig(ctx, orgID, "")
	if err != nil {
		return "", "", err
	}
	if cfg == nil {
		return "", "", fmt.Errorf("Claude not configured")
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	text, err := s.callClaude(ctx, cfg.AnthropicAPIKey, cfg.Model, prompt, maxTokens)
	if err != nil {
		return "", cfg.Model, err
	}
	return text, cfg.Model, nil
}

func (s *ClaudeService) callClaude(ctx context.Context, apiKey, model, prompt string, maxTokens int) (string, error) {
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyJSON))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Anthropic API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Anthropic API %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse Claude response: %w", err)
	}

	for _, c := range result.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in Claude response")
}
