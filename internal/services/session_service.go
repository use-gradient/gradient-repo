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

type SessionService struct {
	db *db.DB
}

func NewSessionService(database *db.DB) *SessionService {
	return &SessionService{db: database}
}

// ─── Agent Sessions ─────────────────────────────────────────────────────

func (s *SessionService) CreateSession(ctx context.Context, session *models.AgentSession) (*models.AgentSession, error) {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	session.CreatedAt = time.Now()

	scopeJSON, err := json.Marshal(session.Scope)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal scope: %w", err)
	}
	contractsJSON, err := json.Marshal(session.Contracts)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contracts: %w", err)
	}

	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO agent_sessions (id, task_id, parent_session_id, org_id, agent_role,
			scope, initial_sha, branch_name, environment_id, status, contracts, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		session.ID, session.TaskID, session.ParentSessionID, session.OrgID,
		session.AgentRole, scopeJSON, session.InitialSHA, session.BranchName,
		session.EnvironmentID, session.Status, contractsJSON, session.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	return session, nil
}

func (s *SessionService) GetSession(ctx context.Context, id string) (*models.AgentSession, error) {
	session := &models.AgentSession{}
	var scopeJSON, contractsJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, task_id, parent_session_id, org_id, agent_role, scope,
			initial_sha, branch_name, environment_id, status, contracts,
			created_at, completed_at
		FROM agent_sessions WHERE id = $1`, id,
	).Scan(
		&session.ID, &session.TaskID, &session.ParentSessionID, &session.OrgID,
		&session.AgentRole, &scopeJSON, &session.InitialSHA, &session.BranchName,
		&session.EnvironmentID, &session.Status, &contractsJSON,
		&session.CreatedAt, &session.CompletedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	json.Unmarshal(scopeJSON, &session.Scope)
	json.Unmarshal(contractsJSON, &session.Contracts)
	return session, nil
}

func (s *SessionService) ListSessionsByTask(ctx context.Context, taskID string) ([]*models.AgentSession, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, task_id, parent_session_id, org_id, agent_role, scope,
			initial_sha, branch_name, environment_id, status, contracts,
			created_at, completed_at
		FROM agent_sessions WHERE task_id = $1 ORDER BY created_at ASC`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*models.AgentSession
	for rows.Next() {
		session := &models.AgentSession{}
		var scopeJSON, contractsJSON []byte
		err := rows.Scan(
			&session.ID, &session.TaskID, &session.ParentSessionID, &session.OrgID,
			&session.AgentRole, &scopeJSON, &session.InitialSHA, &session.BranchName,
			&session.EnvironmentID, &session.Status, &contractsJSON,
			&session.CreatedAt, &session.CompletedAt,
		)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(scopeJSON, &session.Scope)
		json.Unmarshal(contractsJSON, &session.Contracts)
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (s *SessionService) UpdateSessionStatus(ctx context.Context, id, status string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE agent_sessions SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}
	return nil
}

func (s *SessionService) CompleteSession(ctx context.Context, id string) error {
	now := time.Now()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE agent_sessions SET status = 'completed', completed_at = $2
		WHERE id = $1`, id, now)
	if err != nil {
		return fmt.Errorf("failed to complete session: %w", err)
	}
	return nil
}

// ─── Change Bundles ─────────────────────────────────────────────────────

func (s *SessionService) CreateBundle(ctx context.Context, bundle *models.ChangeBundle) (*models.ChangeBundle, error) {
	if bundle.ID == "" {
		bundle.ID = uuid.New().String()
	}
	bundle.CreatedAt = time.Now()

	if bundle.Sequence == 0 {
		var maxSeq *int
		err := s.db.Pool.QueryRow(ctx, `
			SELECT MAX(sequence) FROM change_bundles WHERE session_id = $1`, bundle.SessionID,
		).Scan(&maxSeq)
		if err != nil {
			return nil, fmt.Errorf("failed to determine sequence: %w", err)
		}
		if maxSeq != nil {
			bundle.Sequence = *maxSeq + 1
		} else {
			bundle.Sequence = 1
		}
	}

	contextDiffJSON, err := json.Marshal(bundle.ContextDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal context_diff: %w", err)
	}
	decisionDiffJSON, err := json.Marshal(bundle.DecisionDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal decision_diff: %w", err)
	}
	testResultsJSON, err := json.Marshal(bundle.TestResults)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal test_results: %w", err)
	}
	policyResultsJSON, err := json.Marshal(bundle.PolicyResults)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal policy_results: %w", err)
	}

	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO change_bundles (id, session_id, sequence, git_patch, commit_sha,
			context_diff, decision_diff, test_results, policy_results, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		bundle.ID, bundle.SessionID, bundle.Sequence, bundle.GitPatch, bundle.CommitSHA,
		contextDiffJSON, decisionDiffJSON, testResultsJSON, policyResultsJSON,
		bundle.Status, bundle.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create change bundle: %w", err)
	}
	return bundle, nil
}

func (s *SessionService) GetBundle(ctx context.Context, id string) (*models.ChangeBundle, error) {
	bundle := &models.ChangeBundle{}
	var contextDiffJSON, decisionDiffJSON, testResultsJSON, policyResultsJSON []byte

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, session_id, sequence, git_patch, commit_sha,
			context_diff, decision_diff, test_results, policy_results,
			status, created_at
		FROM change_bundles WHERE id = $1`, id,
	).Scan(
		&bundle.ID, &bundle.SessionID, &bundle.Sequence, &bundle.GitPatch,
		&bundle.CommitSHA, &contextDiffJSON, &decisionDiffJSON,
		&testResultsJSON, &policyResultsJSON, &bundle.Status, &bundle.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get change bundle: %w", err)
	}

	json.Unmarshal(contextDiffJSON, &bundle.ContextDiff)
	json.Unmarshal(decisionDiffJSON, &bundle.DecisionDiff)
	json.Unmarshal(testResultsJSON, &bundle.TestResults)
	json.Unmarshal(policyResultsJSON, &bundle.PolicyResults)
	return bundle, nil
}

func (s *SessionService) ListBundlesBySession(ctx context.Context, sessionID string) ([]*models.ChangeBundle, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, session_id, sequence, git_patch, commit_sha,
			context_diff, decision_diff, test_results, policy_results,
			status, created_at
		FROM change_bundles WHERE session_id = $1 ORDER BY sequence ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list change bundles: %w", err)
	}
	defer rows.Close()

	var bundles []*models.ChangeBundle
	for rows.Next() {
		bundle := &models.ChangeBundle{}
		var contextDiffJSON, decisionDiffJSON, testResultsJSON, policyResultsJSON []byte
		err := rows.Scan(
			&bundle.ID, &bundle.SessionID, &bundle.Sequence, &bundle.GitPatch,
			&bundle.CommitSHA, &contextDiffJSON, &decisionDiffJSON,
			&testResultsJSON, &policyResultsJSON, &bundle.Status, &bundle.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(contextDiffJSON, &bundle.ContextDiff)
		json.Unmarshal(decisionDiffJSON, &bundle.DecisionDiff)
		json.Unmarshal(testResultsJSON, &bundle.TestResults)
		json.Unmarshal(policyResultsJSON, &bundle.PolicyResults)
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

func (s *SessionService) UpdateBundleStatus(ctx context.Context, id, status string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE change_bundles SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("failed to update bundle status: %w", err)
	}
	return nil
}

// ─── Contracts ──────────────────────────────────────────────────────────

func (s *SessionService) CreateContract(ctx context.Context, contract *models.Contract) (*models.Contract, error) {
	if contract.ID == "" {
		contract.ID = uuid.New().String()
	}
	now := time.Now()
	contract.CreatedAt = now
	contract.UpdatedAt = now
	if contract.Version == 0 {
		contract.Version = 1
	}

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO contracts (id, org_id, task_id, type, scope, spec,
			owner_session_id, consumers, version, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		contract.ID, contract.OrgID, contract.TaskID, contract.Type,
		contract.Scope, contract.Spec, contract.OwnerSessionID,
		contract.Consumers, contract.Version, contract.Status,
		contract.CreatedAt, contract.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create contract: %w", err)
	}
	return contract, nil
}

func (s *SessionService) GetContract(ctx context.Context, id string) (*models.Contract, error) {
	contract := &models.Contract{}

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, task_id, type, scope, spec, owner_session_id,
			consumers, version, status, created_at, updated_at
		FROM contracts WHERE id = $1`, id,
	).Scan(
		&contract.ID, &contract.OrgID, &contract.TaskID, &contract.Type,
		&contract.Scope, &contract.Spec, &contract.OwnerSessionID,
		&contract.Consumers, &contract.Version, &contract.Status,
		&contract.CreatedAt, &contract.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get contract: %w", err)
	}
	return contract, nil
}

func (s *SessionService) ListContractsByTask(ctx context.Context, taskID string) ([]*models.Contract, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, task_id, type, scope, spec, owner_session_id,
			consumers, version, status, created_at, updated_at
		FROM contracts WHERE task_id = $1 ORDER BY created_at ASC`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list contracts: %w", err)
	}
	defer rows.Close()

	var contracts []*models.Contract
	for rows.Next() {
		contract := &models.Contract{}
		err := rows.Scan(
			&contract.ID, &contract.OrgID, &contract.TaskID, &contract.Type,
			&contract.Scope, &contract.Spec, &contract.OwnerSessionID,
			&contract.Consumers, &contract.Version, &contract.Status,
			&contract.CreatedAt, &contract.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, contract)
	}
	return contracts, nil
}

func (s *SessionService) AddContractConsumer(ctx context.Context, contractID, sessionID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE contracts
		SET consumers = array_append(consumers, $2), updated_at = NOW()
		WHERE id = $1 AND NOT ($2 = ANY(consumers))`,
		contractID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("failed to add contract consumer: %w", err)
	}
	return nil
}

func (s *SessionService) UpdateContractStatus(ctx context.Context, id, status string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE contracts SET status = $2, updated_at = NOW() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("failed to update contract status: %w", err)
	}
	return nil
}

// ─── Context Objects ────────────────────────────────────────────────────

func (s *SessionService) UpsertContextObject(ctx context.Context, obj *models.ContextObject) (*models.ContextObject, error) {
	if obj.ID == "" {
		obj.ID = uuid.New().String()
	}
	now := time.Now()
	obj.CreatedAt = now
	obj.UpdatedAt = now

	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO context_objects (id, org_id, branch, type, key, value,
			source_session, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (org_id, branch, type, key) DO UPDATE SET
			value = EXCLUDED.value,
			source_session = EXCLUDED.source_session,
			version = context_objects.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING id, version, created_at, updated_at`,
		obj.ID, obj.OrgID, obj.Branch, obj.Type, obj.Key, obj.Value,
		obj.SourceSession, obj.Version, obj.CreatedAt, obj.UpdatedAt,
	).Scan(&obj.ID, &obj.Version, &obj.CreatedAt, &obj.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert context object: %w", err)
	}
	return obj, nil
}

func (s *SessionService) GetContextObject(ctx context.Context, orgID, branch, objType, key string) (*models.ContextObject, error) {
	obj := &models.ContextObject{}

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, branch, type, key, value, source_session,
			version, created_at, updated_at
		FROM context_objects
		WHERE org_id = $1 AND branch = $2 AND type = $3 AND key = $4`,
		orgID, branch, objType, key,
	).Scan(
		&obj.ID, &obj.OrgID, &obj.Branch, &obj.Type, &obj.Key, &obj.Value,
		&obj.SourceSession, &obj.Version, &obj.CreatedAt, &obj.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get context object: %w", err)
	}
	return obj, nil
}

func (s *SessionService) ListContextObjects(ctx context.Context, orgID, branch string, objType string) ([]*models.ContextObject, error) {
	query := `
		SELECT id, org_id, branch, type, key, value, source_session,
			version, created_at, updated_at
		FROM context_objects WHERE org_id = $1 AND branch = $2`
	args := []interface{}{orgID, branch}

	if objType != "" {
		query += " AND type = $3"
		args = append(args, objType)
	}

	query += " ORDER BY type, key"

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list context objects: %w", err)
	}
	defer rows.Close()

	var objects []*models.ContextObject
	for rows.Next() {
		obj := &models.ContextObject{}
		err := rows.Scan(
			&obj.ID, &obj.OrgID, &obj.Branch, &obj.Type, &obj.Key, &obj.Value,
			&obj.SourceSession, &obj.Version, &obj.CreatedAt, &obj.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

func (s *SessionService) ForkContextObjects(ctx context.Context, orgID, fromBranch, toBranch, sessionID string) error {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT org_id, type, key, value FROM context_objects
		WHERE org_id = $1 AND branch = $2`, orgID, fromBranch,
	)
	if err != nil {
		return fmt.Errorf("failed to read source context objects: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var srcOrgID, srcType, srcKey string
		var srcValue json.RawMessage
		if err := rows.Scan(&srcOrgID, &srcType, &srcKey, &srcValue); err != nil {
			return err
		}

		_, err := s.db.Pool.Exec(ctx, `
			INSERT INTO context_objects (id, org_id, branch, type, key, value,
				source_session, version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,$8)
			ON CONFLICT (org_id, branch, type, key) DO UPDATE SET
				value = EXCLUDED.value,
				source_session = EXCLUDED.source_session,
				version = context_objects.version + 1,
				updated_at = EXCLUDED.updated_at`,
			uuid.New().String(), srcOrgID, toBranch, srcType, srcKey,
			srcValue, sessionID, now,
		)
		if err != nil {
			return fmt.Errorf("failed to fork context object %s/%s: %w", srcType, srcKey, err)
		}
	}
	return nil
}
