package services

import (
	"context"
	"encoding/json"
	"fmt"
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
func (s *ClaudeService) SaveConfig(ctx context.Context, orgID, userID, apiKey, model string, maxTurns int, allowedTools []string, maxCost float64) (*models.ClaudeConfig, error) {
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
		MaxCostPerTask:   maxCost,
		MaxTokensPerTask: 100000,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-20250514"
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 50
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
			allowed_tools, max_cost_per_task, max_tokens_per_task, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (org_id, user_id) DO UPDATE SET
			anthropic_api_key=EXCLUDED.anthropic_api_key, model=EXCLUDED.model,
			max_turns=EXCLUDED.max_turns, allowed_tools=EXCLUDED.allowed_tools,
			max_cost_per_task=EXCLUDED.max_cost_per_task, updated_at=NOW()`,
		cfg.ID, cfg.OrgID, userIDPtr, cfg.AnthropicAPIKey, cfg.Model, cfg.MaxTurns,
		string(toolsJSON), cfg.MaxCostPerTask, cfg.MaxTokensPerTask,
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
				COALESCE(max_turns, 50), COALESCE(allowed_tools::text, '["Edit","Write","Bash","Read"]'),
				COALESCE(max_cost_per_task, 0), COALESCE(max_tokens_per_task, 100000),
				created_at, updated_at
			FROM claude_configs
			WHERE org_id = $1 AND (user_id = $2 OR user_id IS NULL)
			ORDER BY user_id DESC NULLS LAST LIMIT 1`
		args = []interface{}{orgID, userID}
	} else {
		query = `
			SELECT id, org_id, user_id, anthropic_api_key, COALESCE(model, 'claude-sonnet-4-20250514'),
				COALESCE(max_turns, 50), COALESCE(allowed_tools::text, '["Edit","Write","Bash","Read"]'),
				COALESCE(max_cost_per_task, 0), COALESCE(max_tokens_per_task, 100000),
				created_at, updated_at
			FROM claude_configs
			WHERE org_id = $1
			ORDER BY user_id NULLS LAST LIMIT 1`
		args = []interface{}{orgID}
	}

	err := s.db.Pool.QueryRow(ctx, query, args...).Scan(
		&cfg.ID, &cfg.OrgID, &nullUserID, &cfg.AnthropicAPIKey, &cfg.Model, &cfg.MaxTurns,
		&toolsJSON, &cfg.MaxCostPerTask, &cfg.MaxTokensPerTask, &cfg.CreatedAt, &cfg.UpdatedAt,
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
