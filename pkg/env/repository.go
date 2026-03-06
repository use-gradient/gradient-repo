package env

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

type Repository struct {
	db *db.DB
}

func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// DB returns the underlying database handle for direct queries.
func (r *Repository) DB() *db.DB {
	return r.db
}

func (r *Repository) Create(ctx context.Context, env *models.Environment) error {
	resourcesJSON, err := json.Marshal(env.Resources)
	if err != nil {
		return fmt.Errorf("failed to marshal resources: %w", err)
	}
	configJSON, err := json.Marshal(env.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		INSERT INTO environments (id, name, org_id, provider, region, size, cluster_name, status, resources, config, context_branch, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err = r.db.Pool.Exec(ctx, query,
		env.ID, env.Name, env.OrgID, env.Provider, env.Region, env.Size, env.ClusterName,
		env.Status, resourcesJSON, configJSON, env.ContextBranch, env.CreatedAt, env.UpdatedAt,
	)
	return err
}

func (r *Repository) GetByID(ctx context.Context, id string) (*models.Environment, error) {
	query := `
		SELECT id, name, org_id, provider, region, size, cluster_name, status, resources, config, context_branch, created_at, updated_at, destroyed_at
		FROM environments
		WHERE id = $1
	`

	var env models.Environment
	var destroyedAt *time.Time
	var resourcesJSON, configJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&env.ID, &env.Name, &env.OrgID, &env.Provider, &env.Region, &env.Size, &env.ClusterName,
		&env.Status, &resourcesJSON, &configJSON, &env.ContextBranch, &env.CreatedAt, &env.UpdatedAt, &destroyedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("environment not found")
	}
	if err != nil {
		return nil, err
	}

	env.DestroyedAt = destroyedAt
	if err := json.Unmarshal(resourcesJSON, &env.Resources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resources: %w", err)
	}
	if configJSON != nil {
		if err := json.Unmarshal(configJSON, &env.Config); err != nil {
			env.Config = map[string]interface{}{} // tolerate bad data
		}
	}
	return &env, nil
}

func (r *Repository) GetByOrgID(ctx context.Context, orgID string) ([]*models.Environment, error) {
	query := `
		SELECT id, name, org_id, provider, region, size, cluster_name, status, resources, config, context_branch, created_at, updated_at, destroyed_at
		FROM environments
		WHERE org_id = $1 AND status != 'destroyed'
		ORDER BY created_at DESC
	`

	rows, err := r.db.Pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []*models.Environment
	for rows.Next() {
		var env models.Environment
		var destroyedAt *time.Time
		var resourcesJSON, configJSON []byte

		err := rows.Scan(
			&env.ID, &env.Name, &env.OrgID, &env.Provider, &env.Region, &env.Size, &env.ClusterName,
			&env.Status, &resourcesJSON, &configJSON, &env.ContextBranch, &env.CreatedAt, &env.UpdatedAt, &destroyedAt,
		)
		if err != nil {
			return nil, err
		}

		env.DestroyedAt = destroyedAt
		if err := json.Unmarshal(resourcesJSON, &env.Resources); err != nil {
			return nil, fmt.Errorf("failed to unmarshal resources: %w", err)
		}
		if configJSON != nil {
			if err := json.Unmarshal(configJSON, &env.Config); err != nil {
				// Tolerate bad config data (e.g. arrays from old schema) — treat as empty
				env.Config = map[string]interface{}{}
			}
		}
		envs = append(envs, &env)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating environments: %w", err)
	}

	return envs, nil
}

func (r *Repository) GetByOrgAndBranch(ctx context.Context, orgID, branch string) (*models.Environment, error) {
	query := `
		SELECT id, name, org_id, provider, region, size, cluster_name, status, resources, config, context_branch, created_at, updated_at, destroyed_at
		FROM environments
		WHERE org_id = $1 AND context_branch = $2 AND status = 'running'
		LIMIT 1
	`

	var env models.Environment
	var destroyedAt *time.Time
	var resourcesJSON, configJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, orgID, branch).Scan(
		&env.ID, &env.Name, &env.OrgID, &env.Provider, &env.Region, &env.Size, &env.ClusterName,
		&env.Status, &resourcesJSON, &configJSON, &env.ContextBranch, &env.CreatedAt, &env.UpdatedAt, &destroyedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil // No running env for this branch — not an error
	}
	if err != nil {
		return nil, err
	}

	env.DestroyedAt = destroyedAt
	if err := json.Unmarshal(resourcesJSON, &env.Resources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resources: %w", err)
	}
	if configJSON != nil {
		if err := json.Unmarshal(configJSON, &env.Config); err != nil {
			env.Config = map[string]interface{}{} // tolerate bad data
		}
	}
	return &env, nil
}

func (r *Repository) Update(ctx context.Context, env *models.Environment) error {
	resourcesJSON, err := json.Marshal(env.Resources)
	if err != nil {
		return fmt.Errorf("failed to marshal resources: %w", err)
	}
	configJSON, err := json.Marshal(env.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		UPDATE environments
		SET name = $2, status = $3, cluster_name = $4, resources = $5, config = $6, context_branch = $7, updated_at = $8, destroyed_at = $9, size = $10
		WHERE id = $1
	`

	_, err = r.db.Pool.Exec(ctx, query,
		env.ID, env.Name, env.Status, env.ClusterName, resourcesJSON, configJSON, env.ContextBranch, env.UpdatedAt, env.DestroyedAt, env.Size,
	)
	return err
}
