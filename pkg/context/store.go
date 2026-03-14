package context

import (
	gocontext "context"
	"encoding/json"
	"fmt"

	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Save(ctx gocontext.Context, c *models.Context) error {
	packagesJSON, err := json.Marshal(c.InstalledPackages)
	if err != nil {
		return fmt.Errorf("failed to marshal packages: %w", err)
	}
	failuresJSON, err := json.Marshal(c.PreviousFailures)
	if err != nil {
		return fmt.Errorf("failed to marshal failures: %w", err)
	}
	fixesJSON, err := json.Marshal(c.AttemptedFixes)
	if err != nil {
		return fmt.Errorf("failed to marshal fixes: %w", err)
	}
	patternsJSON, err := json.Marshal(c.Patterns)
	if err != nil {
		return fmt.Errorf("failed to marshal patterns: %w", err)
	}
	configsJSON, err := json.Marshal(c.GlobalConfigs)
	if err != nil {
		return fmt.Errorf("failed to marshal configs: %w", err)
	}

	query := `
		INSERT INTO contexts (id, branch, org_id, repo_full_name, commit_sha, installed_packages, previous_failures, attempted_fixes, patterns, global_configs, summary_text, change_log_text, base_os, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (org_id, repo_full_name, branch) DO UPDATE SET
			commit_sha = EXCLUDED.commit_sha,
			installed_packages = EXCLUDED.installed_packages,
			previous_failures = EXCLUDED.previous_failures,
			attempted_fixes = EXCLUDED.attempted_fixes,
			patterns = EXCLUDED.patterns,
			global_configs = EXCLUDED.global_configs,
			summary_text = CASE WHEN EXCLUDED.summary_text = '' THEN contexts.summary_text ELSE EXCLUDED.summary_text END,
			change_log_text = CASE WHEN EXCLUDED.change_log_text = '' THEN contexts.change_log_text ELSE EXCLUDED.change_log_text END,
			base_os = EXCLUDED.base_os,
			updated_at = EXCLUDED.updated_at
	`

	_, err = s.db.Pool.Exec(ctx, query,
		c.ID, c.Branch, c.OrgID, c.RepoFullName, c.CommitSHA,
		packagesJSON, failuresJSON, fixesJSON,
		patternsJSON, configsJSON, c.SummaryText, c.ChangeLogText, c.BaseOS, c.CreatedAt, c.UpdatedAt,
	)
	return err
}

func (s *Store) GetByBranch(ctx gocontext.Context, orgID, branch string) (*models.Context, error) {
	query := `
		SELECT id, branch, org_id, COALESCE(repo_full_name, ''), COALESCE(commit_sha, ''), installed_packages, previous_failures, attempted_fixes, patterns, global_configs, COALESCE(summary_text, ''), COALESCE(change_log_text, ''), base_os, created_at, updated_at
		FROM contexts
		WHERE org_id = $1 AND branch = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`

	var c models.Context
	var packagesJSON, failuresJSON, fixesJSON, patternsJSON, configsJSON []byte

	err := s.db.Pool.QueryRow(ctx, query, orgID, branch).Scan(
		&c.ID, &c.Branch, &c.OrgID, &c.RepoFullName, &c.CommitSHA,
		&packagesJSON, &failuresJSON, &fixesJSON,
		&patternsJSON, &configsJSON, &c.SummaryText, &c.ChangeLogText, &c.BaseOS, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("context not found for branch: %s", branch)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	if err := json.Unmarshal(packagesJSON, &c.InstalledPackages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal installed_packages: %w", err)
	}
	if err := json.Unmarshal(failuresJSON, &c.PreviousFailures); err != nil {
		return nil, fmt.Errorf("failed to unmarshal previous_failures: %w", err)
	}
	if err := json.Unmarshal(fixesJSON, &c.AttemptedFixes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attempted_fixes: %w", err)
	}
	if err := json.Unmarshal(patternsJSON, &c.Patterns); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patterns: %w", err)
	}
	if err := json.Unmarshal(configsJSON, &c.GlobalConfigs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global_configs: %w", err)
	}

	return &c, nil
}

func (s *Store) ListByOrg(ctx gocontext.Context, orgID string) ([]*models.Context, error) {
	query := `
		SELECT id, branch, org_id, COALESCE(repo_full_name, ''), COALESCE(commit_sha, ''), installed_packages, previous_failures, attempted_fixes, patterns, global_configs, COALESCE(summary_text, ''), COALESCE(change_log_text, ''), base_os, created_at, updated_at
		FROM contexts
		WHERE org_id = $1
		ORDER BY updated_at DESC
	`

	rows, err := s.db.Pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to list contexts: %w", err)
	}
	defer rows.Close()

	var contexts []*models.Context
	for rows.Next() {
		var c models.Context
		var packagesJSON, failuresJSON, fixesJSON, patternsJSON, configsJSON []byte

		err := rows.Scan(
			&c.ID, &c.Branch, &c.OrgID, &c.RepoFullName, &c.CommitSHA,
			&packagesJSON, &failuresJSON, &fixesJSON,
			&patternsJSON, &configsJSON, &c.SummaryText, &c.ChangeLogText, &c.BaseOS, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan context: %w", err)
		}

		if err := json.Unmarshal(packagesJSON, &c.InstalledPackages); err != nil {
			return nil, fmt.Errorf("failed to unmarshal installed_packages: %w", err)
		}
		if err := json.Unmarshal(failuresJSON, &c.PreviousFailures); err != nil {
			return nil, fmt.Errorf("failed to unmarshal previous_failures: %w", err)
		}
		if err := json.Unmarshal(fixesJSON, &c.AttemptedFixes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attempted_fixes: %w", err)
		}
		if err := json.Unmarshal(patternsJSON, &c.Patterns); err != nil {
			return nil, fmt.Errorf("failed to unmarshal patterns: %w", err)
		}
		if err := json.Unmarshal(configsJSON, &c.GlobalConfigs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal global_configs: %w", err)
		}

		contexts = append(contexts, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating contexts: %w", err)
	}

	return contexts, nil
}

func (s *Store) Delete(ctx gocontext.Context, orgID, branch string) error {
	query := `DELETE FROM contexts WHERE org_id = $1 AND branch = $2`
	_, err := s.db.Pool.Exec(ctx, query, orgID, branch)
	return err
}

func (s *Store) GetByRepoBranch(ctx gocontext.Context, orgID, repoFullName, branch string) (*models.Context, error) {
	query := `
		SELECT id, branch, org_id, COALESCE(repo_full_name, ''), COALESCE(commit_sha, ''), installed_packages, previous_failures, attempted_fixes, patterns, global_configs, COALESCE(summary_text, ''), COALESCE(change_log_text, ''), base_os, created_at, updated_at
		FROM contexts
		WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`

	var c models.Context
	var packagesJSON, failuresJSON, fixesJSON, patternsJSON, configsJSON []byte

	err := s.db.Pool.QueryRow(ctx, query, orgID, repoFullName, branch).Scan(
		&c.ID, &c.Branch, &c.OrgID, &c.RepoFullName, &c.CommitSHA,
		&packagesJSON, &failuresJSON, &fixesJSON,
		&patternsJSON, &configsJSON, &c.SummaryText, &c.ChangeLogText, &c.BaseOS, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("context not found for repo %s branch: %s", repoFullName, branch)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	if err := json.Unmarshal(packagesJSON, &c.InstalledPackages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal installed_packages: %w", err)
	}
	if err := json.Unmarshal(failuresJSON, &c.PreviousFailures); err != nil {
		return nil, fmt.Errorf("failed to unmarshal previous_failures: %w", err)
	}
	if err := json.Unmarshal(fixesJSON, &c.AttemptedFixes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attempted_fixes: %w", err)
	}
	if err := json.Unmarshal(patternsJSON, &c.Patterns); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patterns: %w", err)
	}
	if err := json.Unmarshal(configsJSON, &c.GlobalConfigs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global_configs: %w", err)
	}

	return &c, nil
}

func (s *Store) DeleteByRepoBranch(ctx gocontext.Context, orgID, repoFullName, branch string) error {
	query := `DELETE FROM contexts WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`
	_, err := s.db.Pool.Exec(ctx, query, orgID, repoFullName, branch)
	return err
}

func (s *Store) UpdateMaterialized(ctx gocontext.Context, orgID, repoFullName, branch, summaryText, changeLogText string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO contexts (
			id, branch, org_id, repo_full_name, commit_sha, installed_packages, previous_failures,
			attempted_fixes, patterns, global_configs, summary_text, change_log_text, base_os, created_at, updated_at
		)
		VALUES (
			md5(random()::text || clock_timestamp()::text), $3, $1, $2, '', '[]'::jsonb, '[]'::jsonb,
			'[]'::jsonb, '{}'::jsonb, '{}'::jsonb, $4, $5, 'ubuntu-24.04', NOW(), NOW()
		)
		ON CONFLICT (org_id, repo_full_name, branch) DO UPDATE SET
			summary_text = EXCLUDED.summary_text,
			change_log_text = EXCLUDED.change_log_text,
			updated_at = NOW()
	`, orgID, repoFullName, branch, summaryText, changeLogText)
	return err
}
