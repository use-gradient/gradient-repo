package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

// TaskService orchestrates the full agent-task lifecycle:
// Linear issue → provision env → run Claude Code → save context → PR → report back
type TaskService struct {
	db              *db.DB
	envService      *EnvService
	claudeService   *ClaudeService
	linearService   *LinearService
	contextService  *ContextService
	defaultProvider string // from DEV_ENV_SRC config
	defaultRegion   string // provider-specific default region
}

func NewTaskService(
	database *db.DB,
	envService *EnvService,
	claudeService *ClaudeService,
	linearService *LinearService,
	contextService *ContextService,
	defaultRegion string,
) *TaskService {
	defaultProvider := "aws"
	if envService != nil && envService.config.DefaultProvider != "" {
		defaultProvider = envService.config.DefaultProvider
	}
	if defaultRegion == "" {
		if defaultProvider == "hetzner" {
			defaultRegion = "fsn1"
		} else {
			defaultRegion = "us-east-2"
		}
	}
	return &TaskService{
		db:              database,
		envService:      envService,
		claudeService:   claudeService,
		linearService:   linearService,
		defaultProvider: defaultProvider,
		defaultRegion:   defaultRegion,
		contextService:  contextService,
	}
}

// ─── Task CRUD ──────────────────────────────────────────────────────────

// ReadinessCheck returns whether the org is ready to run tasks and what's missing.
type ReadinessStatus struct {
	Ready             bool   `json:"ready"`
	ClaudeConfigured  bool   `json:"claude_configured"`
	LinearConnected   bool   `json:"linear_connected"`
	Message           string `json:"message,omitempty"`
}

func (s *TaskService) CheckReadiness(ctx context.Context, orgID string) *ReadinessStatus {
	rs := &ReadinessStatus{}

	rs.ClaudeConfigured = s.claudeService.HasConfig(ctx, orgID)

	conn, err := s.linearService.GetConnection(ctx, orgID)
	rs.LinearConnected = err == nil && conn != nil

	rs.Ready = rs.ClaudeConfigured // Claude is the hard requirement

	if !rs.ClaudeConfigured {
		rs.Message = "Configure your Anthropic API key in Integrations → Claude Code before creating tasks."
	}

	return rs
}

func (s *TaskService) CreateTask(ctx context.Context, orgID string, req CreateTaskRequest) (*models.AgentTask, error) {
	// ── Preflight: Claude must be configured ──
	if !s.claudeService.HasConfig(ctx, orgID) {
		return nil, fmt.Errorf("Claude Code is not configured. Add your Anthropic API key in Integrations before creating tasks.")
	}

	task := &models.AgentTask{
		ID:               uuid.New().String(),
		OrgID:            orgID,
		ParentTaskID:     req.ParentTaskID,
		Title:            req.Title,
		Description:      req.Description,
		Prompt:           req.Prompt,
		Branch:           req.Branch,
		RepoFullName:     req.RepoFullName,
		LinearIssueID:    req.LinearIssueID,
		LinearIdentifier: req.LinearIdentifier,
		LinearURL:        req.LinearURL,
		Status:           "pending",
		MaxRetries:       2,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if task.Prompt == "" {
		task.Prompt = task.Title
		if task.Description != "" {
			task.Prompt = task.Title + "\n\n" + task.Description
		}
	}

	var parentTaskID interface{} = nil
	if task.ParentTaskID != "" {
		parentTaskID = task.ParentTaskID
	}

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO agent_tasks (id, org_id, parent_task_id, linear_issue_id, linear_identifier, linear_url,
			title, description, prompt, branch, repo_full_name, status,
			max_retries, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		task.ID, task.OrgID, parentTaskID, task.LinearIssueID, task.LinearIdentifier, task.LinearURL,
		task.Title, task.Description, task.Prompt, task.Branch, task.RepoFullName, task.Status,
		task.MaxRetries, task.CreatedAt, task.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	s.addLog(ctx, task.ID, "created", "completed", "Task created", nil)
	return task, nil
}

type CreateTaskRequest struct {
	Title            string `json:"title"`
	Description      string `json:"description"`
	Prompt           string `json:"prompt"`
	Branch           string `json:"branch"`
	RepoFullName     string `json:"repo_full_name"`
	ParentTaskID     string `json:"parent_task_id"`
	LinearIssueID    string `json:"linear_issue_id"`
	LinearIdentifier string `json:"linear_identifier"`
	LinearURL        string `json:"linear_url"`
}

func (s *TaskService) GetTask(ctx context.Context, orgID, taskID string) (*models.AgentTask, error) {
	task := &models.AgentTask{}
	var outputJSON *string
	var parentTaskID *string

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, parent_task_id,
			COALESCE(linear_issue_id, ''), COALESCE(linear_identifier, ''), COALESCE(linear_url, ''),
			title, COALESCE(description, ''), COALESCE(prompt, ''),
			COALESCE(environment_id, ''), COALESCE(branch, ''), COALESCE(repo_full_name, ''),
			status, COALESCE(output_summary, ''), output_json,
			COALESCE(commit_sha, ''), COALESCE(pr_url, ''), COALESCE(error_message, ''),
			started_at, completed_at,
			COALESCE(duration_seconds, 0), COALESCE(tokens_used, 0), COALESCE(estimated_cost, 0),
			COALESCE(retry_count, 0), COALESCE(max_retries, 2), COALESCE(context_saved, false), COALESCE(snapshot_taken, false),
			created_at, updated_at
		FROM agent_tasks WHERE id = $1 AND org_id = $2`, taskID, orgID,
	).Scan(
		&task.ID, &task.OrgID, &parentTaskID,
		&task.LinearIssueID, &task.LinearIdentifier, &task.LinearURL,
		&task.Title, &task.Description, &task.Prompt, &task.EnvironmentID, &task.Branch, &task.RepoFullName,
		&task.Status, &task.OutputSummary, &outputJSON, &task.CommitSHA, &task.PRURL, &task.ErrorMessage,
		&task.StartedAt, &task.CompletedAt, &task.DurationSeconds, &task.TokensUsed, &task.EstimatedCost,
		&task.RetryCount, &task.MaxRetries, &task.ContextSaved, &task.SnapshotTaken, &task.CreatedAt, &task.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if parentTaskID != nil {
		task.ParentTaskID = *parentTaskID
	}
	if outputJSON != nil {
		json.Unmarshal([]byte(*outputJSON), &task.OutputJSON)
	}
	return task, nil
}

func (s *TaskService) TaskExistsForLinearIssue(ctx context.Context, orgID, linearIssueID string) (bool, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE org_id = $1 AND linear_issue_id = $2`,
		orgID, linearIssueID,
	).Scan(&count)
	return count > 0, err
}

func (s *TaskService) ListTasks(ctx context.Context, orgID, status string, limit int) ([]*models.AgentTask, error) {
	query := `SELECT id, org_id, parent_task_id,
		COALESCE(linear_issue_id, ''), COALESCE(linear_identifier, ''), COALESCE(linear_url, ''),
		title, COALESCE(description, ''), COALESCE(prompt, ''),
		COALESCE(environment_id, ''), COALESCE(branch, ''), COALESCE(repo_full_name, ''),
		status, COALESCE(output_summary, ''), COALESCE(commit_sha, ''), COALESCE(pr_url, ''), COALESCE(error_message, ''),
		started_at, completed_at,
		COALESCE(duration_seconds, 0), COALESCE(tokens_used, 0), COALESCE(estimated_cost, 0),
		COALESCE(retry_count, 0), COALESCE(max_retries, 2), COALESCE(context_saved, false), COALESCE(snapshot_taken, false),
		created_at, updated_at
		FROM agent_tasks WHERE org_id = $1`

	args := []interface{}{orgID}
	argN := 2

	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, status)
		argN++
	}

	query += " ORDER BY created_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, limit)
	}

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*models.AgentTask
	for rows.Next() {
		task := &models.AgentTask{}
		var parentTaskID *string
		err := rows.Scan(
			&task.ID, &task.OrgID, &parentTaskID,
			&task.LinearIssueID, &task.LinearIdentifier, &task.LinearURL,
			&task.Title, &task.Description, &task.Prompt, &task.EnvironmentID, &task.Branch, &task.RepoFullName,
			&task.Status, &task.OutputSummary, &task.CommitSHA, &task.PRURL, &task.ErrorMessage,
			&task.StartedAt, &task.CompletedAt, &task.DurationSeconds, &task.TokensUsed, &task.EstimatedCost,
			&task.RetryCount, &task.MaxRetries, &task.ContextSaved, &task.SnapshotTaken, &task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if parentTaskID != nil {
			task.ParentTaskID = *parentTaskID
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *TaskService) DeleteAllTasks(ctx context.Context, orgID string) (int64, error) {
	// Delete logs first (foreign key-like dependency)
	_, _ = s.db.Pool.Exec(ctx, `
		DELETE FROM task_execution_log WHERE task_id IN (
			SELECT id FROM agent_tasks WHERE org_id = $1
		)`, orgID)

	result, err := s.db.Pool.Exec(ctx, `DELETE FROM agent_tasks WHERE org_id = $1`, orgID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *TaskService) CancelTask(ctx context.Context, orgID, taskID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE agent_tasks SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND status IN ('pending','queued','running')`,
		taskID, orgID,
	)
	if err != nil {
		return err
	}
	s.addLog(ctx, taskID, "cancelled", "completed", "Task cancelled by user", nil)
	return nil
}

func (s *TaskService) RetryTask(ctx context.Context, orgID, taskID string) (*models.AgentTask, error) {
	task, err := s.GetTask(ctx, orgID, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task not found")
	}
	if task.Status != "failed" && task.Status != "cancelled" {
		return nil, fmt.Errorf("can only retry failed or cancelled tasks")
	}

	_, err = s.db.Pool.Exec(ctx, `
		UPDATE agent_tasks SET status = 'pending', error_message = '',
			retry_count = retry_count + 1, updated_at = NOW()
		WHERE id = $1 AND org_id = $2`,
		taskID, orgID,
	)
	if err != nil {
		return nil, err
	}

	s.addLog(ctx, taskID, "retried", "completed", fmt.Sprintf("Retry #%d", task.RetryCount+1), nil)
	task.Status = "pending"
	task.ErrorMessage = ""
	return task, nil
}

// ─── Execution Flow ─────────────────────────────────────────────────────

func (s *TaskService) StartTaskExecution(ctx context.Context, orgID, taskID string) error {
	task, err := s.GetTask(ctx, orgID, taskID)
	if err != nil || task == nil {
		return fmt.Errorf("task not found")
	}
	if task.Status != "pending" && task.Status != "queued" {
		return fmt.Errorf("task not in pending/queued state: %s", task.Status)
	}

	if !s.claudeService.HasConfig(ctx, orgID) {
		return s.failTask(ctx, taskID, "Claude Code not configured. Add your Anthropic API key in Settings → Integrations.")
	}

	now := time.Now()
	_, err = s.db.Pool.Exec(ctx, `
		UPDATE agent_tasks SET status = 'running', started_at = $2, updated_at = NOW()
		WHERE id = $1`, taskID, now)
	if err != nil {
		return err
	}
	s.addLog(ctx, taskID, "execution_started", "started", "Task execution began", nil)

	if task.LinearIssueID != "" {
		go func() {
			bgCtx := context.Background()
			s.linearService.UpdateIssueState(bgCtx, orgID, task.LinearIssueID, "In Progress")
			s.linearService.AddComment(bgCtx, orgID, task.LinearIssueID,
				fmt.Sprintf("🤖 Gradient agent started working on this issue.\n\nTask ID: `%s`", taskID))
		}()
	}

	s.addLog(ctx, taskID, "queued_for_execution", "completed", "Task queued for Claude Code execution", nil)
	return nil
}

func (s *TaskService) CompleteTask(ctx context.Context, orgID, taskID string, result CompleteTaskRequest) error {
	now := time.Now()
	outputJSON, _ := json.Marshal(result.OutputJSON)

	_, err := s.db.Pool.Exec(ctx, `
		UPDATE agent_tasks SET
			status = 'complete', output_summary = $3, output_json = $4,
			commit_sha = $5, pr_url = $6, completed_at = $7,
			duration_seconds = EXTRACT(EPOCH FROM ($7::timestamp - started_at))::int,
			tokens_used = $8, estimated_cost = $9,
			context_saved = $10, snapshot_taken = $11,
			updated_at = NOW()
		WHERE id = $1 AND org_id = $2`,
		taskID, orgID, result.OutputSummary, string(outputJSON),
		result.CommitSHA, result.PRURL, now,
		result.TokensUsed, result.EstimatedCost,
		result.ContextSaved, result.SnapshotTaken,
	)
	if err != nil {
		return err
	}

	s.addLog(ctx, taskID, "completed", "completed", result.OutputSummary, nil)

	task, _ := s.GetTask(ctx, orgID, taskID)
	if task != nil && task.LinearIssueID != "" {
		go func() {
			bgCtx := context.Background()
			summary := result.OutputSummary
			if result.PRURL != "" {
				summary += fmt.Sprintf("\n\nPR: %s", result.PRURL)
			}
			s.linearService.UpdateIssueState(bgCtx, orgID, task.LinearIssueID, "Done")
			s.linearService.AddComment(bgCtx, orgID, task.LinearIssueID,
				fmt.Sprintf("✅ Gradient agent completed this task.\n\n%s", summary))
		}()
	}

	s.checkAndCompleteParent(ctx, orgID, taskID)
	return nil
}

type CompleteTaskRequest struct {
	OutputSummary string                 `json:"output_summary"`
	OutputJSON    map[string]interface{} `json:"output_json"`
	CommitSHA     string                 `json:"commit_sha"`
	PRURL         string                 `json:"pr_url"`
	TokensUsed    int                    `json:"tokens_used"`
	EstimatedCost float64                `json:"estimated_cost"`
	ContextSaved  bool                   `json:"context_saved"`
	SnapshotTaken bool                   `json:"snapshot_taken"`
}

func (s *TaskService) FailTask(ctx context.Context, orgID, taskID, errorMsg string) error {
	if err := s.failTask(ctx, taskID, errorMsg); err != nil {
		return err
	}
	s.checkAndCompleteParent(ctx, orgID, taskID)
	return nil
}

func (s *TaskService) failTask(ctx context.Context, taskID, errorMsg string) error {
	now := time.Now()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE agent_tasks SET status = 'failed', error_message = $2, completed_at = $3,
			duration_seconds = CASE WHEN started_at IS NOT NULL
				THEN EXTRACT(EPOCH FROM ($3::timestamp - started_at))::int ELSE 0 END,
			updated_at = NOW()
		WHERE id = $1`, taskID, errorMsg, now)
	if err != nil {
		return err
	}
	s.addLog(ctx, taskID, "failed", "failed", errorMsg, nil)
	return nil
}

// checkAndCompleteParent checks if the given task has a parent, and if all siblings
// are terminal, marks the parent as complete (or failed if any sub-task failed).
func (s *TaskService) checkAndCompleteParent(ctx context.Context, orgID, taskID string) {
	var parentID *string
	s.db.Pool.QueryRow(ctx, `SELECT parent_task_id FROM agent_tasks WHERE id = $1`, taskID).Scan(&parentID)
	if parentID == nil || *parentID == "" {
		return
	}

	var total, terminal, failed int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status IN ('complete', 'failed', 'cancelled')),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM agent_tasks WHERE parent_task_id = $1`, *parentID,
	).Scan(&total, &terminal, &failed)
	if err != nil || total == 0 || terminal < total {
		return
	}

	now := time.Now()
	if failed > 0 {
		_, _ = s.db.Pool.Exec(ctx, `
			UPDATE agent_tasks SET status = 'failed',
				error_message = $2, completed_at = $3,
				duration_seconds = CASE WHEN started_at IS NOT NULL
					THEN EXTRACT(EPOCH FROM ($3::timestamp - started_at))::int ELSE 0 END,
				updated_at = NOW()
			WHERE id = $1 AND status = 'running'`,
			*parentID, fmt.Sprintf("%d of %d sub-tasks failed", failed, total), now)
		s.addLog(ctx, *parentID, "completed", "failed",
			fmt.Sprintf("Parent task failed: %d/%d sub-tasks failed", failed, total), nil)
		log.Printf("[task] Parent task %s marked failed (%d/%d sub-tasks failed)", *parentID, failed, total)
	} else {
		_, _ = s.db.Pool.Exec(ctx, `
			UPDATE agent_tasks SET status = 'complete', completed_at = $2,
				duration_seconds = CASE WHEN started_at IS NOT NULL
					THEN EXTRACT(EPOCH FROM ($2::timestamp - started_at))::int ELSE 0 END,
				output_summary = $3, updated_at = NOW()
			WHERE id = $1 AND status = 'running'`,
			*parentID, now, fmt.Sprintf("All %d sub-tasks completed successfully", total))
		s.addLog(ctx, *parentID, "completed", "completed",
			fmt.Sprintf("All %d sub-tasks completed", total), nil)
		log.Printf("[task] Parent task %s marked complete (all %d sub-tasks done)", *parentID, total)
	}
}

// ─── Task Logs ──────────────────────────────────────────────────────────

func (s *TaskService) addLog(ctx context.Context, taskID, step, status, message string, metadata map[string]interface{}) {
	metaJSON := "{}"
	if metadata != nil {
		b, _ := json.Marshal(metadata)
		metaJSON = string(b)
	}
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO task_execution_log (id, task_id, step, status, message, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		uuid.New().String(), taskID, step, status, message, metaJSON,
	)
	if err != nil {
		log.Printf("[task] failed to add log for task %s: %v", taskID, err)
	}
}

func (s *TaskService) GetTaskLogs(ctx context.Context, orgID, taskID string) ([]*models.TaskLogEntry, error) {
	var count int
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM agent_tasks WHERE id = $1 AND org_id = $2`, taskID, orgID).Scan(&count)
	if count == 0 {
		return nil, fmt.Errorf("task not found")
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, task_id, step, status, message, metadata, created_at
		FROM task_execution_log WHERE task_id = $1 ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*models.TaskLogEntry
	for rows.Next() {
		entry := &models.TaskLogEntry{}
		var metaJSON string
		err := rows.Scan(&entry.ID, &entry.TaskID, &entry.Step, &entry.Status, &entry.Message, &metaJSON, &entry.CreatedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(metaJSON), &entry.Metadata)
		logs = append(logs, entry)
	}
	return logs, nil
}

// ─── Task Settings ──────────────────────────────────────────────────────

func (s *TaskService) GetSettings(ctx context.Context, orgID string) (*models.TaskSettings, error) {
	settings := &models.TaskSettings{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT org_id, instance_strategy, max_concurrent_tasks, default_env_size,
			default_env_provider, default_env_region, auto_create_pr, pr_base_branch,
			auto_destroy_env, env_ttl_minutes
		FROM task_settings WHERE org_id = $1`, orgID,
	).Scan(
		&settings.OrgID, &settings.InstanceStrategy, &settings.MaxConcurrentTasks,
		&settings.DefaultEnvSize, &settings.DefaultEnvProvider, &settings.DefaultEnvRegion,
		&settings.AutoCreatePR, &settings.PRBaseBranch,
		&settings.AutoDestroyEnv, &settings.EnvTTLMinutes,
	)
	if err == pgx.ErrNoRows {
		return &models.TaskSettings{
			OrgID:              orgID,
			InstanceStrategy:   "one_per_task",
			MaxConcurrentTasks: 3,
			DefaultEnvSize:     "small",
			DefaultEnvProvider: s.defaultProvider,
			DefaultEnvRegion:   s.defaultRegion,
			AutoCreatePR:       true,
			PRBaseBranch:       "main",
			AutoDestroyEnv:     true,
			EnvTTLMinutes:      30,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *TaskService) SaveSettings(ctx context.Context, settings *models.TaskSettings) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO task_settings (org_id, instance_strategy, max_concurrent_tasks,
			default_env_size, default_env_provider, default_env_region,
			auto_create_pr, pr_base_branch, auto_destroy_env, env_ttl_minutes,
			created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())
		ON CONFLICT (org_id) DO UPDATE SET
			instance_strategy=EXCLUDED.instance_strategy,
			max_concurrent_tasks=EXCLUDED.max_concurrent_tasks,
			default_env_size=EXCLUDED.default_env_size,
			default_env_provider=EXCLUDED.default_env_provider,
			default_env_region=EXCLUDED.default_env_region,
			auto_create_pr=EXCLUDED.auto_create_pr,
			pr_base_branch=EXCLUDED.pr_base_branch,
			auto_destroy_env=EXCLUDED.auto_destroy_env,
			env_ttl_minutes=EXCLUDED.env_ttl_minutes,
			updated_at=NOW()`,
		settings.OrgID, settings.InstanceStrategy, settings.MaxConcurrentTasks,
		settings.DefaultEnvSize, settings.DefaultEnvProvider, settings.DefaultEnvRegion,
		settings.AutoCreatePR, settings.PRBaseBranch,
		settings.AutoDestroyEnv, settings.EnvTTLMinutes,
	)
	return err
}

// ─── Stats ──────────────────────────────────────────────────────────────

type TaskStats struct {
	Total       int     `json:"total"`
	Pending     int     `json:"pending"`
	Running     int     `json:"running"`
	Complete    int     `json:"complete"`
	Failed      int     `json:"failed"`
	Cancelled   int     `json:"cancelled"`
	AvgDuration float64 `json:"avg_duration_seconds"`
	TotalCost   float64 `json:"total_cost"`
}

func (s *TaskService) GetStats(ctx context.Context, orgID string) (*TaskStats, error) {
	stats := &TaskStats{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'complete'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'cancelled'),
			COALESCE(AVG(duration_seconds) FILTER (WHERE status = 'complete'), 0),
			COALESCE(SUM(estimated_cost), 0)
		FROM agent_tasks WHERE org_id = $1`, orgID,
	).Scan(
		&stats.Total, &stats.Pending, &stats.Running, &stats.Complete,
		&stats.Failed, &stats.Cancelled, &stats.AvgDuration, &stats.TotalCost,
	)
	if err != nil {
		return nil, err
	}
	return stats, nil
}
