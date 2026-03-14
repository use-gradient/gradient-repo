package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

type MemoryRetrieveRequest struct {
	OrgID            string
	RepoFullName     string
	Branch           string
	TaskID           string
	SessionID        string
	TaskTitle        string
	TaskDescription  string
	TaskPrompt       string
	Subtask          string
	FailureSignature string
	Limit            int
}

type RetrievedTip struct {
	Tip    *models.MemoryTip `json:"tip"`
	Score  float64           `json:"score"`
	Reason string            `json:"reason,omitempty"`
}

type MaterializedBranchContext struct {
	SummaryText   string `json:"summary_text"`
	ChangeLogText string `json:"change_log_text"`
	DocumentText  string `json:"document_text"`
}

type TrajectoryMemoryService struct {
	db                *db.DB
	contextService    *ContextService
	sessionService    *SessionService
	claudeService     *ClaudeService
	analysisService   *TrajectoryAnalysisService
	retrievalService  *MemoryRetrievalService
	embeddingProvider EmbeddingProvider
}

func NewTrajectoryMemoryService(database *db.DB, contextService *ContextService, sessionService *SessionService, claudeService *ClaudeService, embeddingProvider EmbeddingProvider) *TrajectoryMemoryService {
	if embeddingProvider == nil {
		embeddingProvider = NewNullEmbeddingProvider()
	}
	svc := &TrajectoryMemoryService{
		db:                database,
		contextService:    contextService,
		sessionService:    sessionService,
		claudeService:     claudeService,
		embeddingProvider: embeddingProvider,
	}
	svc.analysisService = NewTrajectoryAnalysisService(database, claudeService, sessionService)
	svc.retrievalService = NewMemoryRetrievalService(database, claudeService, embeddingProvider)
	return svc
}

func (s *TrajectoryMemoryService) SeedTipsFromContext(ctx context.Context, repoFullName, branch string, ctxObj *models.Context) error {
	if ctxObj == nil || ctxObj.OrgID == "" || repoFullName == "" {
		return nil
	}

	for key, value := range ctxObj.Patterns {
		content := fmt.Sprintf("Repository history indicates the `%s` pattern: %v. Reuse it before introducing a competing approach.", key, value)
		_, err := s.upsertTip(ctx, tipCandidate{
			OrgID:            ctxObj.OrgID,
			RepoFullName:     repoFullName,
			SourceBranch:     branch,
			TipType:          "strategy",
			Scope:            "task",
			Title:            fmt.Sprintf("Bootstrap pattern: %s", key),
			Content:          content,
			TriggerCondition: fmt.Sprintf("When the task touches workflow or code related to %s", key),
			ActionSteps: []string{
				"Inspect the existing implementation path before editing.",
				fmt.Sprintf("Prefer the established `%s` pattern unless there is a clear need to replace it.", key),
				"Verify that new changes still fit the repository's existing conventions.",
			},
			Priority:        "low",
			Confidence:      0.25,
			CanonicalKey:    "seed-pattern:" + normalizedKey(key),
			Keywords:        uniqueTokens(append(tokenize(key), tokenize(fmt.Sprint(value))...)),
			SearchText:      normalizeText(key + " " + fmt.Sprint(value)),
			OutcomeClass:    "bootstrap",
			TaskFingerprint: normalizedKey(key),
			SourceKind:      "context_bootstrap",
		})
		if err != nil {
			return err
		}
	}

	for _, failure := range ctxObj.PreviousFailures {
		failureSignature := normalizedKey(failure.Test + " " + failure.Error)
		content := fmt.Sprintf("Previous branch history recorded `%s` failing with `%s`. Verify or reproduce that path before assuming it is stable.", failure.Test, failure.Error)
		_, err := s.upsertTip(ctx, tipCandidate{
			OrgID:            ctxObj.OrgID,
			RepoFullName:     repoFullName,
			SourceBranch:     branch,
			TipType:          "recovery",
			Scope:            "task",
			Title:            fmt.Sprintf("Bootstrap failure guard: %s", failure.Test),
			Content:          content,
			TriggerCondition: fmt.Sprintf("When task work could affect %s or related flows", failure.Test),
			ActionSteps: []string{
				fmt.Sprintf("Re-run or inspect `%s` before finalizing related changes.", failure.Test),
				"Address the recorded failure mode before retrying the broader workflow.",
				"Capture the updated outcome so the branch memory can improve.",
			},
			Priority:         "low",
			Confidence:       0.25,
			CanonicalKey:     "seed-failure:" + failureSignature,
			FailureSignature: failureSignature,
			Keywords:         uniqueTokens(append(tokenize(failure.Test), tokenize(failure.Error)...)),
			SearchText:       normalizeText(failure.Test + " " + failure.Error),
			OutcomeClass:     "bootstrap",
			TaskFingerprint:  normalizedKey(failure.Test),
			SourceKind:       "context_bootstrap",
		})
		if err != nil {
			return err
		}
	}

	for _, fix := range ctxObj.AttemptedFixes {
		if strings.TrimSpace(fix.Fix) == "" || !fix.Success {
			continue
		}
		_, err := s.upsertTip(ctx, tipCandidate{
			OrgID:            ctxObj.OrgID,
			RepoFullName:     repoFullName,
			SourceBranch:     branch,
			TipType:          "strategy",
			Scope:            "task",
			Title:            "Bootstrap successful fix",
			Content:          fmt.Sprintf("A previous branch iteration recorded this successful fix pattern: %s", fix.Fix),
			TriggerCondition: "When similar regressions or failures appear on this branch",
			ActionSteps: []string{
				"Compare the current regression with the previously successful fix.",
				"Reuse the proven fix path if the failure mode matches.",
				"Validate with the most relevant targeted test before broad verification.",
			},
			Priority:        "low",
			Confidence:      0.25,
			CanonicalKey:    "seed-fix:" + normalizedKey(fix.Fix),
			Keywords:        tokenize(fix.Fix),
			SearchText:      normalizeText(fix.Fix),
			OutcomeClass:    "bootstrap",
			TaskFingerprint: normalizedKey(branch),
			SourceKind:      "context_bootstrap",
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *TrajectoryMemoryService) GenerateTipsForTask(ctx context.Context, orgID, taskID string) ([]*models.MemoryTip, error) {
	task, err := s.getTask(ctx, orgID, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.RepoFullName == "" {
		return nil, nil
	}
	if shouldSkipTrajectoryMemory(task) {
		return nil, nil
	}

	if s.analysisService != nil {
		if analysis, err := s.analysisService.AnalyzeTask(ctx, orgID, taskID); err == nil && analysis != nil {
			tips, genErr := s.generateTipsFromAnalysis(ctx, analysis)
			if genErr == nil && len(tips) > 0 {
				return tips, nil
			}
		}
	}
	return s.generateHeuristicTipsForTask(ctx, orgID, taskID)
}

func (s *TrajectoryMemoryService) generateHeuristicTipsForTask(ctx context.Context, orgID, taskID string) ([]*models.MemoryTip, error) {
	task, err := s.getTask(ctx, orgID, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.RepoFullName == "" {
		return nil, nil
	}

	logs, err := s.getTaskLogs(ctx, taskID)
	if err != nil {
		return nil, err
	}
	logs = latestAttemptLogs(task, logs)

	sessions, err := s.sessionService.ListSessionsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	sessions = latestAttemptSessions(task, logs, sessions)

	var bundles []*models.ChangeBundle
	sessionIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		sessionIDs = append(sessionIDs, session.ID)
		items, err := s.sessionService.ListBundlesBySession(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, items...)
	}

	events, err := s.getTrajectoryEvents(ctx, task, sessionIDs)
	if err != nil {
		return nil, err
	}

	subtasks := collectSubtasks(task, logs, bundles, events)
	hasFailure := trajectoryHasFailure(task, logs, subtasks)
	hasRecovery := trajectoryHasRecovery(subtasks)
	taskKeywords := uniqueTokens(tokenize(strings.Join([]string{task.Title, task.Description, task.Prompt}, " ")))
	taskFingerprint := normalizedKey(task.Title + " " + task.Description)

	var candidates []tipCandidate
	if task.Status == "complete" {
		if hasRecovery {
			for _, st := range subtasks {
				if !st.Failed || !st.Succeeded {
					continue
				}
				failureSignature := st.failureSignature()
				trigger := fmt.Sprintf("When working on %s and `%s` appears again", st.displayName(), valueOrDefault(failureSignature, "the same failure mode"))
				candidates = append(candidates, tipCandidate{
					OrgID:            orgID,
					RepoFullName:     task.RepoFullName,
					SourceBranch:     task.Branch,
					TipType:          "recovery",
					Scope:            st.scope(),
					Title:            fmt.Sprintf("Recovery for %s", st.displayName()),
					Content:          buildRecoveryContent(st),
					TriggerCondition: trigger,
					ActionSteps:      recoverySteps(st),
					Priority:         "high",
					Confidence:       0.8,
					CanonicalKey:     "recovery:" + normalizedKey(st.displayName()+" "+failureSignature),
					FailureSignature: failureSignature,
					Keywords:         uniqueTokens(append(taskKeywords, st.keywords()...)),
					SearchText:       normalizeText(st.displayName() + " " + st.Summary + " " + failureSignature),
					OutcomeClass:     "recovered",
					TaskFingerprint:  taskFingerprint,
					SessionIDs:       st.SessionIDs,
					BundleIDs:        st.BundleIDs,
					EventIDs:         st.EventIDs,
					TaskID:           task.ID,
				})
			}
		}

		if !hasFailure || len(candidates) == 0 {
			for _, st := range subtasks {
				if st.Failed || !st.Succeeded {
					continue
				}
				candidates = append(candidates, tipCandidate{
					OrgID:            orgID,
					RepoFullName:     task.RepoFullName,
					SourceBranch:     task.Branch,
					TipType:          "strategy",
					Scope:            st.scope(),
					Title:            fmt.Sprintf("Strategy for %s", st.displayName()),
					Content:          buildStrategyContent(st, task),
					TriggerCondition: fmt.Sprintf("When the task involves %s", st.displayName()),
					ActionSteps:      strategySteps(st),
					Priority:         "medium",
					Confidence:       0.72,
					CanonicalKey:     "strategy:" + normalizedKey(st.displayName()),
					Keywords:         uniqueTokens(append(taskKeywords, st.keywords()...)),
					SearchText:       normalizeText(st.displayName() + " " + st.Summary + " " + task.Title),
					OutcomeClass:     "clean_success",
					TaskFingerprint:  taskFingerprint,
					SessionIDs:       st.SessionIDs,
					BundleIDs:        st.BundleIDs,
					EventIDs:         st.EventIDs,
					TaskID:           task.ID,
				})
			}
		}

		for _, st := range subtasks {
			if st.Repetitions < 2 || !st.Succeeded {
				continue
			}
			candidates = append(candidates, tipCandidate{
				OrgID:            orgID,
				RepoFullName:     task.RepoFullName,
				SourceBranch:     task.Branch,
				TipType:          "optimization",
				Scope:            st.scope(),
				Title:            fmt.Sprintf("Optimization for %s", st.displayName()),
				Content:          buildOptimizationContent(st),
				TriggerCondition: fmt.Sprintf("When %s requires repeated retries or loops", st.displayName()),
				ActionSteps:      optimizationSteps(st),
				Priority:         "medium",
				Confidence:       0.65,
				CanonicalKey:     "optimization:" + normalizedKey(st.displayName()),
				Keywords:         uniqueTokens(append(taskKeywords, st.keywords()...)),
				SearchText:       normalizeText(st.displayName() + " " + st.Summary),
				OutcomeClass:     "inefficient_success",
				TaskFingerprint:  taskFingerprint,
				SessionIDs:       st.SessionIDs,
				BundleIDs:        st.BundleIDs,
				EventIDs:         st.EventIDs,
				TaskID:           task.ID,
			})
		}

		if len(candidates) == 0 {
			candidates = append(candidates, tipCandidate{
				OrgID:            orgID,
				RepoFullName:     task.RepoFullName,
				SourceBranch:     task.Branch,
				TipType:          "strategy",
				Scope:            "task",
				Title:            "Task completion strategy",
				Content:          fmt.Sprintf("For tasks similar to `%s`, keep work scoped, validate the changed area, and capture subtask boundaries so follow-on runs can reuse the trajectory.", task.Title),
				TriggerCondition: "When a similar repository task is starting",
				ActionSteps: []string{
					"Inspect the existing code path before editing.",
					"Work in scoped subtasks and verify each subtask before moving on.",
					"Capture outcomes so future runs can retrieve the proven path.",
				},
				Priority:        "medium",
				Confidence:      0.5,
				CanonicalKey:    "strategy:task:" + taskFingerprint,
				Keywords:        taskKeywords,
				SearchText:      normalizeText(task.Title + " " + task.Description),
				OutcomeClass:    "clean_success",
				TaskFingerprint: taskFingerprint,
				SessionIDs:      sessionIDs,
				TaskID:          task.ID,
			})
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	candidates = dedupeCandidates(candidates)
	tips := make([]*models.MemoryTip, 0, len(candidates))
	for _, candidate := range candidates {
		tip, err := s.upsertTip(ctx, candidate)
		if err != nil {
			return nil, err
		}
		tips = append(tips, tip)
	}

	return tips, nil
}

func (s *TrajectoryMemoryService) RetrieveGuidance(ctx context.Context, req MemoryRetrieveRequest) ([]RetrievedTip, error) {
	if s.retrievalService == nil {
		return nil, nil
	}
	return s.retrievalService.RetrieveGuidance(ctx, req)
}

func (s *TrajectoryMemoryService) FormatGuidanceForPrompt(items []RetrievedTip) string {
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Retrieved Guidance\n\n")
	for _, item := range items {
		tip := item.Tip
		sb.WriteString(fmt.Sprintf("[%s][%s] %s\n", strings.ToUpper(tip.Priority), strings.ToUpper(tip.TipType), tip.Title))
		sb.WriteString(fmt.Sprintf("- Apply when: %s\n", valueOrDefault(tip.TriggerCondition, "task context is similar to this prior trajectory")))
		sb.WriteString(fmt.Sprintf("- Guidance: %s\n", tip.Content))
		if item.Reason != "" {
			sb.WriteString(fmt.Sprintf("- Why selected: %s\n", item.Reason))
		}
		if len(tip.ActionSteps) > 0 {
			for i, step := range tip.ActionSteps {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func (s *TrajectoryMemoryService) BuildGuidanceSnapshot(items []RetrievedTip) map[string]interface{} {
	snapshot := map[string]interface{}{
		"retrieved_at": time.Now().UTC().Format(time.RFC3339),
		"tips":         []map[string]interface{}{},
	}

	tips := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		tips = append(tips, map[string]interface{}{
			"id":                item.Tip.ID,
			"title":             item.Tip.Title,
			"type":              item.Tip.TipType,
			"priority":          item.Tip.Priority,
			"content":           item.Tip.Content,
			"trigger_condition": item.Tip.TriggerCondition,
			"action_steps":      item.Tip.ActionSteps,
			"failure_signature": item.Tip.FailureSignature,
			"score":             item.Score,
			"reason":            item.Reason,
		})
	}
	snapshot["tips"] = tips

	return snapshot
}

func (s *TrajectoryMemoryService) BuildMaterializedContext(ctx context.Context, orgID, repoFullName, branch string) (*MaterializedBranchContext, error) {
	if orgID == "" || repoFullName == "" || branch == "" {
		return &MaterializedBranchContext{}, nil
	}

	var (
		ctxObj    *models.Context
		tips      []*models.MemoryTip
		analyses  []*models.TrajectoryAnalysis
		runs      []*models.RetrievalRun
		sessions  []*models.AgentSession
		events    []*trajectoryEvent
		taskItems []*models.AgentTask
	)

	if s.contextService != nil {
		ctxObj, _ = s.contextService.GetRepoContext(ctx, orgID, repoFullName, branch)
	}
	tips, _ = s.listTipsByRepoBranch(ctx, orgID, repoFullName, branch, 8)
	analyses, _ = s.listAnalysesByRepoBranch(ctx, orgID, repoFullName, branch, 6)
	runs, _ = s.listRetrievalRunsByRepoBranch(ctx, orgID, repoFullName, branch, 4)
	sessions, _ = s.listRecentSessionsByRepoBranch(ctx, orgID, repoFullName, branch, 6)
	taskItems, _ = s.listRecentTasksByRepoBranch(ctx, orgID, repoFullName, branch, 8)
	events, _ = s.listRecentEventsByRepoBranch(ctx, orgID, repoFullName, branch, 8)

	summaryText := s.renderContextSummary(ctxObj, taskItems, tips, analyses, runs)
	changeLogText := s.renderContextChangeLog(taskItems, sessions, events)

	return &MaterializedBranchContext{
		SummaryText:   summaryText,
		ChangeLogText: changeLogText,
		DocumentText:  strings.TrimSpace(strings.Join([]string{summaryText, changeLogText}, "\n\n")),
	}, nil
}

func (s *TrajectoryMemoryService) RefreshMaterializedContext(ctx context.Context, orgID, repoFullName, branch string) (*MaterializedBranchContext, error) {
	materialized, err := s.BuildMaterializedContext(ctx, orgID, repoFullName, branch)
	if err != nil {
		return nil, err
	}
	if s.contextService != nil {
		if updateErr := s.contextService.UpdateMaterializedContext(ctx, orgID, repoFullName, branch, materialized.SummaryText, materialized.ChangeLogText); updateErr != nil {
			return nil, updateErr
		}
	}
	return materialized, nil
}

func (s *TrajectoryMemoryService) ListTipsByRepo(ctx context.Context, orgID, repoFullName string, limit int) ([]*models.MemoryTip, error) {
	if orgID == "" || repoFullName == "" {
		return []*models.MemoryTip{}, nil
	}
	if limit <= 0 {
		limit = 25
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, repo_full_name, source_branch, tip_type, scope, title, content,
		       COALESCE(trigger_condition, ''), action_steps, priority, confidence, canonical_key,
		       COALESCE(failure_signature, ''), COALESCE(task_fingerprint, ''), keywords,
		       COALESCE(search_text, ''), COALESCE(semantic_summary, ''), COALESCE(outcome_class, ''),
		       COALESCE(embedding_status, 'disabled'), COALESCE(embedding_model, ''), embedding_updated_at,
		       evidence_count, use_count, last_retrieved_at, created_at, updated_at
		FROM memory_tips
		WHERE org_id = $1 AND repo_full_name = $2
		ORDER BY updated_at DESC
		LIMIT $3
	`, orgID, repoFullName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list memory tips: %w", err)
	}
	defer rows.Close()

	tips := make([]*models.MemoryTip, 0, limit)
	for rows.Next() {
		tip, err := scanMemoryTip(rows)
		if err != nil {
			return nil, err
		}
		tips = append(tips, tip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating memory tips: %w", err)
	}
	return tips, nil
}

func (s *TrajectoryMemoryService) GetTipsByIDs(ctx context.Context, orgID, repoFullName string, ids []string) ([]*models.MemoryTip, error) {
	ids = uniqueStrings(ids)
	if orgID == "" || repoFullName == "" || len(ids) == 0 {
		return []*models.MemoryTip{}, nil
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, repo_full_name, source_branch, tip_type, scope, title, content,
		       COALESCE(trigger_condition, ''), action_steps, priority, confidence, canonical_key,
		       COALESCE(failure_signature, ''), COALESCE(task_fingerprint, ''), keywords,
		       COALESCE(search_text, ''), COALESCE(semantic_summary, ''), COALESCE(outcome_class, ''),
		       COALESCE(embedding_status, 'disabled'), COALESCE(embedding_model, ''), embedding_updated_at,
		       evidence_count, use_count, last_retrieved_at, created_at, updated_at
		FROM memory_tips
		WHERE org_id = $1 AND repo_full_name = $2 AND id = ANY($3)
	`, orgID, repoFullName, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory tips by id: %w", err)
	}
	defer rows.Close()

	tips := make([]*models.MemoryTip, 0, len(ids))
	for rows.Next() {
		tip, err := scanMemoryTip(rows)
		if err != nil {
			return nil, err
		}
		tips = append(tips, tip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating memory tips by id: %w", err)
	}
	return tips, nil
}

func (s *TrajectoryMemoryService) ListAnalysesByRepo(ctx context.Context, orgID, repoFullName string, limit int) ([]*models.TrajectoryAnalysis, error) {
	if orgID == "" || repoFullName == "" {
		return []*models.TrajectoryAnalysis{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, repo_full_name, task_id, COALESCE(session_id::text, ''), COALESCE(source_branch, ''),
		       COALESCE(trajectory_summary, ''), COALESCE(outcome_class, ''), COALESCE(immediate_cause, ''),
		       COALESCE(proximate_cause, ''), COALESCE(root_cause, ''), COALESCE(recovery_action, ''),
		       COALESCE(recovery_reason, ''), COALESCE(inefficiency_pattern, ''), COALESCE(recommended_alternative, ''),
		       subtask_analyses, COALESCE(analyzer_version, ''), COALESCE(model_name, ''), confidence,
		       created_at, updated_at
		FROM trajectory_analyses
		WHERE org_id = $1 AND repo_full_name = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, orgID, repoFullName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list trajectory analyses: %w", err)
	}
	defer rows.Close()

	analyses := make([]*models.TrajectoryAnalysis, 0, limit)
	for rows.Next() {
		analysis, err := scanTrajectoryAnalysis(rows)
		if err != nil {
			return nil, err
		}
		analyses = append(analyses, analysis)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating trajectory analyses: %w", err)
	}
	return analyses, nil
}

func (s *TrajectoryMemoryService) ListRetrievalRunsByRepo(ctx context.Context, orgID, repoFullName string, limit int) ([]*models.RetrievalRun, error) {
	if orgID == "" || repoFullName == "" {
		return []*models.RetrievalRun{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, repo_full_name, COALESCE(task_id, ''), COALESCE(session_id::text, ''),
		       COALESCE(query_text, ''), COALESCE(subtask, ''), COALESCE(failure_signature, ''),
		       candidate_tip_ids, reranked_tip_ids, selected_tip_ids, vector_search_used,
		       COALESCE(reranker_model, ''), COALESCE(status, ''), latency_ms, created_at
		FROM retrieval_runs
		WHERE org_id = $1 AND repo_full_name = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, orgID, repoFullName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list retrieval runs: %w", err)
	}
	defer rows.Close()

	runs := make([]*models.RetrievalRun, 0, limit)
	for rows.Next() {
		run, err := scanRetrievalRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating retrieval runs: %w", err)
	}
	return runs, nil
}

func (s *TrajectoryMemoryService) listTipsByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*models.MemoryTip, error) {
	tips, err := s.ListTipsByRepo(ctx, orgID, repoFullName, limit*3)
	if err != nil {
		return nil, err
	}
	filtered := make([]*models.MemoryTip, 0, minInt(len(tips), limit))
	for _, tip := range tips {
		if tip == nil {
			continue
		}
		if tip.SourceBranch != "" && tip.SourceBranch != branch {
			continue
		}
		filtered = append(filtered, tip)
		if len(filtered) >= limit {
			break
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	if len(tips) > limit {
		return tips[:limit], nil
	}
	return tips, nil
}

func (s *TrajectoryMemoryService) listAnalysesByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*models.TrajectoryAnalysis, error) {
	analyses, err := s.ListAnalysesByRepo(ctx, orgID, repoFullName, limit*3)
	if err != nil {
		return nil, err
	}
	filtered := make([]*models.TrajectoryAnalysis, 0, minInt(len(analyses), limit))
	for _, analysis := range analyses {
		if analysis == nil {
			continue
		}
		if analysis.SourceBranch != "" && analysis.SourceBranch != branch {
			continue
		}
		filtered = append(filtered, analysis)
		if len(filtered) >= limit {
			break
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	if len(analyses) > limit {
		return analyses[:limit], nil
	}
	return analyses, nil
}

func (s *TrajectoryMemoryService) listRetrievalRunsByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*models.RetrievalRun, error) {
	runs, err := s.ListRetrievalRunsByRepo(ctx, orgID, repoFullName, limit*3)
	if err != nil {
		return nil, err
	}
	filtered := make([]*models.RetrievalRun, 0, minInt(len(runs), limit))
	for _, run := range runs {
		if run == nil {
			continue
		}
		if branch != "" && run.QueryText != "" && !strings.Contains(strings.ToLower(run.QueryText), strings.ToLower(branch)) && run.Subtask == "" && run.FailureSignature == "" {
			continue
		}
		filtered = append(filtered, run)
		if len(filtered) >= limit {
			break
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	if len(runs) > limit {
		return runs[:limit], nil
	}
	return runs, nil
}

func (s *TrajectoryMemoryService) listRecentSessionsByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*models.AgentSession, error) {
	sessions, err := s.sessionService.ListRecentSessionsByRepo(ctx, orgID, repoFullName, limit*3)
	if err != nil {
		return nil, err
	}
	filtered := make([]*models.AgentSession, 0, minInt(len(sessions), limit))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if session.BranchName != "" && session.BranchName != branch {
			continue
		}
		filtered = append(filtered, session)
		if len(filtered) >= limit {
			break
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	if len(sessions) > limit {
		return sessions[:limit], nil
	}
	return sessions, nil
}

func (s *TrajectoryMemoryService) listRecentTasksByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*models.AgentTask, error) {
	if orgID == "" || repoFullName == "" {
		return []*models.AgentTask{}, nil
	}
	if limit <= 0 {
		limit = 8
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, COALESCE(parent_task_id, ''), COALESCE(linear_issue_id, ''),
		       COALESCE(linear_identifier, ''), COALESCE(linear_url, ''), title,
		       COALESCE(description, ''), COALESCE(prompt, ''), COALESCE(environment_id, ''),
		       COALESCE(branch, ''), COALESCE(repo_full_name, ''), status,
		       COALESCE(output_summary, ''), COALESCE(output_json, '{}'::jsonb), COALESCE(commit_sha, ''),
		       COALESCE(pr_url, ''), COALESCE(error_message, ''), started_at, completed_at,
		       COALESCE(duration_seconds, 0), COALESCE(tokens_used, 0), COALESCE(estimated_cost, 0),
		       COALESCE(retry_count, 0), COALESCE(max_retries, 0), COALESCE(context_saved, false),
		       COALESCE(snapshot_taken, false), created_at, updated_at
		FROM agent_tasks
		WHERE org_id = $1 AND COALESCE(repo_full_name, '') = $2 AND COALESCE(branch, '') = $3
		ORDER BY updated_at DESC
		LIMIT $4
	`, orgID, repoFullName, branch, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list repo tasks: %w", err)
	}
	defer rows.Close()

	items := make([]*models.AgentTask, 0, limit)
	for rows.Next() {
		task := &models.AgentTask{}
		var outputJSON []byte
		if err := rows.Scan(
			&task.ID, &task.OrgID, &task.ParentTaskID, &task.LinearIssueID, &task.LinearIdentifier,
			&task.LinearURL, &task.Title, &task.Description, &task.Prompt, &task.EnvironmentID,
			&task.Branch, &task.RepoFullName, &task.Status, &task.OutputSummary, &outputJSON,
			&task.CommitSHA, &task.PRURL, &task.ErrorMessage, &task.StartedAt, &task.CompletedAt,
			&task.DurationSeconds, &task.TokensUsed, &task.EstimatedCost, &task.RetryCount,
			&task.MaxRetries, &task.ContextSaved, &task.SnapshotTaken, &task.CreatedAt, &task.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan repo task: %w", err)
		}
		_ = json.Unmarshal(outputJSON, &task.OutputJSON)
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating repo tasks: %w", err)
	}
	return items, nil
}

func (s *TrajectoryMemoryService) listRecentEventsByRepoBranch(ctx context.Context, orgID, repoFullName, branch string, limit int) ([]*trajectoryEvent, error) {
	if orgID == "" || repoFullName == "" || branch == "" {
		return []*trajectoryEvent{}, nil
	}
	if limit <= 0 {
		limit = 8
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, event_type, data, created_at
		FROM context_events
		WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3
		ORDER BY created_at DESC
		LIMIT $4
	`, orgID, repoFullName, branch, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list repo events: %w", err)
	}
	defer rows.Close()

	items := make([]*trajectoryEvent, 0, limit)
	for rows.Next() {
		var (
			id        string
			eventType string
			dataJSON  []byte
			createdAt time.Time
		)
		if err := rows.Scan(&id, &eventType, &dataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan repo event: %w", err)
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(dataJSON, &raw); err != nil {
			continue
		}
		items = append(items, &trajectoryEvent{
			ID:               id,
			EventType:        eventType,
			SessionID:        stringValue(raw["session_id"]),
			TaskID:           stringValue(raw["task_id"]),
			Subtask:          stringValue(raw["subtask"]),
			Outcome:          stringValue(raw["outcome"]),
			Summary:          firstNonEmpty(stringValue(raw["message"]), stringValue(raw["summary"])),
			FailureSignature: normalizedKey(firstNonEmpty(stringValue(raw["failure_signature"]), stringValue(raw["error"]))),
			RelatedFiles:     stringSliceValue(raw["related_files"]),
			CreatedAt:        createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating repo events: %w", err)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *TrajectoryMemoryService) renderContextSummary(ctxObj *models.Context, tasks []*models.AgentTask, tips []*models.MemoryTip, analyses []*models.TrajectoryAnalysis, runs []*models.RetrievalRun) string {
	var sb strings.Builder
	sb.WriteString("## Repo Branch Context\n\n")
	if ctxObj != nil {
		sb.WriteString(fmt.Sprintf("- Branch: `%s`\n", ctxObj.Branch))
		if ctxObj.CommitSHA != "" {
			sb.WriteString(fmt.Sprintf("- Last recorded commit: `%s`\n", ctxObj.CommitSHA))
		}
		if ctxObj.BaseOS != "" {
			sb.WriteString(fmt.Sprintf("- Base OS: `%s`\n", ctxObj.BaseOS))
		}
		sb.WriteString(fmt.Sprintf("- Last updated: %s\n", ctxObj.UpdatedAt.UTC().Format(time.RFC3339)))
		if len(ctxObj.InstalledPackages) > 0 {
			names := make([]string, 0, len(ctxObj.InstalledPackages))
			for _, pkg := range ctxObj.InstalledPackages {
				names = append(names, pkg.Name)
			}
			sb.WriteString("\n### Installed Packages\n")
			for _, name := range firstNStrings(names, 10) {
				sb.WriteString(fmt.Sprintf("- %s\n", name))
			}
		}
		if len(ctxObj.GlobalConfigs) > 0 {
			sb.WriteString("\n### Environment Config\n")
			keys := make([]string, 0, len(ctxObj.GlobalConfigs))
			for key := range ctxObj.GlobalConfigs {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range firstNStrings(keys, 8) {
				sb.WriteString(fmt.Sprintf("- %s=%s\n", key, ctxObj.GlobalConfigs[key]))
			}
		}
	}
	if len(tasks) > 0 {
		sb.WriteString("\n### Recent Task Outcomes\n")
		for _, task := range firstNTasks(tasks, 4) {
			outcome := firstNonEmpty(task.OutputSummary, task.ErrorMessage, task.Title)
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", strings.ToUpper(task.Status), truncate(outcome, 240)))
		}
	}
	if len(analyses) > 0 {
		sb.WriteString("\n### Attributed Learnings\n")
		for _, analysis := range firstNAnalyses(analyses, 3) {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", strings.ToUpper(valueOrDefault(analysis.OutcomeClass, "unknown")), truncate(firstNonEmpty(analysis.TrajectorySummary, analysis.RootCause, analysis.ImmediateCause), 220)))
		}
	}
	if len(tips) > 0 {
		sb.WriteString("\n### Durable Guidance Already Learned\n")
		for _, tip := range firstNTips(tips, 4) {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", strings.ToUpper(tip.TipType), tip.Title, truncate(tip.Content, 180)))
		}
	}
	if len(runs) > 0 {
		sb.WriteString("\n### Recent Retrieval Behavior\n")
		for _, run := range firstNRuns(runs, 3) {
			desc := firstNonEmpty(run.Subtask, run.FailureSignature, run.QueryText)
			sb.WriteString(fmt.Sprintf("- Retrieved %d tip(s) for %s\n", len(run.SelectedTipIDs), truncate(desc, 120)))
		}
	}
	return strings.TrimSpace(sb.String())
}

func (s *TrajectoryMemoryService) renderContextChangeLog(tasks []*models.AgentTask, sessions []*models.AgentSession, events []*trajectoryEvent) string {
	lines := make([]string, 0, 24)
	for _, task := range firstNTasks(tasks, 5) {
		reason := firstNonEmpty(task.OutputSummary, task.ErrorMessage, task.Title)
		lines = append(lines, fmt.Sprintf("- %s task `%s` [%s]: %s", task.UpdatedAt.UTC().Format("2006-01-02 15:04"), task.ID[:8], strings.ToUpper(task.Status), truncate(reason, 220)))
	}
	for _, session := range firstNSessions(sessions, 5) {
		lines = append(lines, fmt.Sprintf("- %s session `%s` on branch `%s` closed as `%s`", session.CreatedAt.UTC().Format("2006-01-02 15:04"), session.ID[:8], valueOrDefault(session.BranchName, "unknown"), valueOrDefault(session.Status, "active")))
	}
	for _, event := range firstNEvents(events, 8) {
		reason := firstNonEmpty(event.Summary, event.Subtask, event.EventType)
		lines = append(lines, fmt.Sprintf("- %s %s: %s", event.CreatedAt.UTC().Format("2006-01-02 15:04"), valueOrDefault(event.EventType, "event"), truncate(reason, 220)))
	}
	if len(lines) == 0 {
		lines = append(lines, "- No prior branch changes have been materialized yet.")
	}
	return "## Recent Change Log\n\n" + strings.Join(lines, "\n")
}

func firstNTasks(items []*models.AgentTask, n int) []*models.AgentTask {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNAnalyses(items []*models.TrajectoryAnalysis, n int) []*models.TrajectoryAnalysis {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNTips(items []*models.MemoryTip, n int) []*models.MemoryTip {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNRuns(items []*models.RetrievalRun, n int) []*models.RetrievalRun {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNSessions(items []*models.AgentSession, n int) []*models.AgentSession {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func firstNEvents(items []*trajectoryEvent, n int) []*trajectoryEvent {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func (s *TrajectoryMemoryService) generateTipsFromAnalysis(ctx context.Context, analysis *models.TrajectoryAnalysis) ([]*models.MemoryTip, error) {
	if analysis == nil || analysis.OrgID == "" || analysis.RepoFullName == "" {
		return nil, nil
	}

	var candidates []tipCandidate
	taskFingerprint := normalizedKey(analysis.TrajectorySummary)
	addCandidate := func(candidate tipCandidate) {
		candidate.OrgID = analysis.OrgID
		candidate.RepoFullName = analysis.RepoFullName
		candidate.SourceBranch = analysis.SourceBranch
		candidate.TaskID = analysis.TaskID
		if analysis.SessionID != "" {
			candidate.SessionIDs = []string{analysis.SessionID}
		}
		candidate.TaskFingerprint = firstNonEmpty(candidate.TaskFingerprint, taskFingerprint)
		candidate.SemanticSummary = firstNonEmpty(candidate.SemanticSummary, analysis.TrajectorySummary, candidate.Content)
		candidates = append(candidates, candidate)
	}

	switch analysis.OutcomeClass {
	case "recovered":
		addCandidate(tipCandidate{
			TipType:          "recovery",
			Scope:            "task",
			Title:            "Task recovery guidance",
			Content:          firstNonEmpty(analysis.RecoveryAction, analysis.TrajectorySummary, "Use the recorded recovery sequence before retrying."),
			TriggerCondition: fmt.Sprintf("When the task hits the failure pattern `%s` or a similar blocked state", normalizedKey(analysis.ImmediateCause)),
			ActionSteps: []string{
				firstNonEmpty(analysis.ImmediateCause, "Confirm the current failure mode."),
				firstNonEmpty(analysis.RecoveryAction, "Apply the corrective action that changed the task state."),
				"Retry only after the failing condition is different.",
			},
			Priority:         "high",
			Confidence:       analysis.Confidence,
			CanonicalKey:     "analysis-recovery:" + normalizedKey(analysis.RootCause+" "+analysis.RecoveryAction),
			FailureSignature: normalizedKey(analysis.ImmediateCause),
			Keywords:         uniqueTokens(tokenize(analysis.TrajectorySummary + " " + analysis.RootCause + " " + analysis.RecoveryAction)),
			SearchText:       normalizeText(analysis.TrajectorySummary + " " + analysis.RootCause + " " + analysis.RecoveryAction),
			OutcomeClass:     analysis.OutcomeClass,
			SourceKind:       "trajectory_analysis",
		})
	case "inefficient_success":
		addCandidate(tipCandidate{
			TipType:          "optimization",
			Scope:            "task",
			Title:            "Task optimization guidance",
			Content:          firstNonEmpty(analysis.RecommendedAlternative, analysis.TrajectorySummary, "Reuse the more efficient path captured in this trajectory."),
			TriggerCondition: "When the same workflow starts repeating or looping.",
			ActionSteps: []string{
				firstNonEmpty(analysis.InefficiencyPattern, "Spot the repeated loop early."),
				firstNonEmpty(analysis.RecommendedAlternative, "Choose the more direct path."),
				"Validate the focused change before broad verification.",
			},
			Priority:     "medium",
			Confidence:   analysis.Confidence,
			CanonicalKey: "analysis-optimization:" + normalizedKey(analysis.InefficiencyPattern+" "+analysis.RecommendedAlternative),
			Keywords:     uniqueTokens(tokenize(analysis.TrajectorySummary + " " + analysis.InefficiencyPattern + " " + analysis.RecommendedAlternative)),
			SearchText:   normalizeText(analysis.TrajectorySummary + " " + analysis.InefficiencyPattern),
			OutcomeClass: analysis.OutcomeClass,
			SourceKind:   "trajectory_analysis",
		})
	default:
		addCandidate(tipCandidate{
			TipType:          "strategy",
			Scope:            "task",
			Title:            "Task strategy guidance",
			Content:          firstNonEmpty(analysis.TrajectorySummary, analysis.RootCause, "Reuse the validated task strategy from this trajectory."),
			TriggerCondition: "When a similar task or repository flow begins.",
			ActionSteps: []string{
				firstNonEmpty(analysis.RootCause, "Start from the validated path."),
				"Keep the change scoped and verify the local outcome.",
				"Capture the finished subtask before moving on.",
			},
			Priority:     "medium",
			Confidence:   analysis.Confidence,
			CanonicalKey: "analysis-strategy:" + normalizedKey(analysis.TrajectorySummary+" "+analysis.RootCause),
			Keywords:     uniqueTokens(tokenize(analysis.TrajectorySummary + " " + analysis.RootCause)),
			SearchText:   normalizeText(analysis.TrajectorySummary),
			OutcomeClass: analysis.OutcomeClass,
			SourceKind:   "trajectory_analysis",
		})
	}

	for _, subtask := range analysis.SubtaskAnalyses {
		scope := "subtask"
		tipType := "strategy"
		content := firstNonEmpty(subtask.Summary, analysis.TrajectorySummary)
		priority := "medium"
		switch subtask.OutcomeClass {
		case "recovered", "failure":
			tipType = "recovery"
			content = firstNonEmpty(subtask.RecoveryAction, subtask.Summary, "Use the recorded recovery sequence.")
			priority = "high"
		case "inefficient_success":
			tipType = "optimization"
			content = firstNonEmpty(subtask.RecommendedAlternative, subtask.InefficiencyPattern, subtask.Summary)
		}
		steps := []string{
			firstNonEmpty(subtask.ImmediateCause, "Check the current condition before acting."),
			firstNonEmpty(subtask.RecoveryAction, subtask.RecommendedAlternative, "Apply the proven next step."),
			"Verify the subtask result before proceeding.",
		}
		addCandidate(tipCandidate{
			TipType:          tipType,
			Scope:            scope,
			Title:            fmt.Sprintf("%s for %s", strings.Title(tipType), subtask.Name),
			Content:          content,
			TriggerCondition: fmt.Sprintf("When the task involves %s", subtask.Name),
			ActionSteps:      steps,
			Priority:         priority,
			Confidence:       analysis.Confidence,
			CanonicalKey:     fmt.Sprintf("analysis-%s:%s", tipType, normalizedKey(subtask.Name+" "+subtask.FailureSignature+" "+content)),
			FailureSignature: normalizedKey(subtask.FailureSignature),
			Keywords:         uniqueTokens(append(tokenize(subtask.Name+" "+content+" "+subtask.Summary), subtask.RelatedFiles...)),
			SearchText:       normalizeText(subtask.Name + " " + content + " " + subtask.Summary),
			SemanticSummary:  firstNonEmpty(subtask.Summary, content),
			OutcomeClass:     subtask.OutcomeClass,
			BundleIDs:        nil,
			EventIDs:         nil,
			SourceKind:       "trajectory_analysis",
		})
	}

	if len(candidates) == 0 {
		return nil, nil
	}
	candidates = dedupeCandidates(candidates)
	tips := make([]*models.MemoryTip, 0, len(candidates))
	for _, candidate := range candidates {
		tip, err := s.upsertTip(ctx, candidate)
		if err != nil {
			return nil, err
		}
		tips = append(tips, tip)
	}
	return tips, nil
}

func (s *TrajectoryMemoryService) SyncEmbeddingsForTips(ctx context.Context, tips []*models.MemoryTip) error {
	if len(tips) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tips))
	for _, tip := range tips {
		if tip != nil && tip.ID != "" {
			ids = append(ids, tip.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if s.embeddingProvider == nil || !s.embeddingProvider.Enabled() {
		_, err := s.db.Pool.Exec(ctx, `
			UPDATE memory_tips
			SET embedding_status = 'disabled', embedding_model = '', embedding_updated_at = NOW()
			WHERE id = ANY($1)
		`, ids)
		return err
	}

	inputs := make([]TipEmbeddingInput, 0, len(tips))
	for _, tip := range tips {
		if tip == nil || tip.ID == "" {
			continue
		}
		text := firstNonEmpty(tip.SemanticSummary, tip.Content, tip.Title)
		inputs = append(inputs, TipEmbeddingInput{TipID: tip.ID, Text: text})
	}
	embeddings, err := s.embeddingProvider.EmbedTips(ctx, inputs)
	if err != nil {
		_, _ = s.db.Pool.Exec(ctx, `
			UPDATE memory_tips
			SET embedding_status = 'error', embedding_model = $2, embedding_updated_at = NOW()
			WHERE id = ANY($1)
		`, ids, s.embeddingProvider.ModelName())
		return err
	}
	if len(embeddings) == 0 {
		return nil
	}

	hasVectorColumn, err := s.hasVectorEmbeddingColumn(ctx)
	if err != nil {
		return err
	}
	for idx, input := range inputs {
		if idx >= len(embeddings) {
			break
		}
		embedding := embeddings[idx]
		if hasVectorColumn {
			if err := s.upsertVectorEmbedding(ctx, input.TipID, embedding); err != nil {
				return err
			}
			_, err = s.db.Pool.Exec(ctx, `
				UPDATE memory_tips
				SET embedding_status = 'embedded', embedding_model = $2, embedding_updated_at = NOW()
				WHERE id = $1
			`, input.TipID, s.embeddingProvider.ModelName())
		} else {
			if err := s.upsertJSONEmbedding(ctx, input.TipID, embedding); err != nil {
				return err
			}
			_, err = s.db.Pool.Exec(ctx, `
				UPDATE memory_tips
				SET embedding_status = 'embedded_json', embedding_model = $2, embedding_updated_at = NOW()
				WHERE id = $1
			`, input.TipID, s.embeddingProvider.ModelName())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type tipCandidate struct {
	OrgID            string
	RepoFullName     string
	SourceBranch     string
	TipType          string
	Scope            string
	Title            string
	Content          string
	TriggerCondition string
	ActionSteps      []string
	Priority         string
	Confidence       float64
	CanonicalKey     string
	FailureSignature string
	TaskFingerprint  string
	Keywords         []string
	SearchText       string
	SemanticSummary  string
	OutcomeClass     string
	TaskID           string
	SessionIDs       []string
	BundleIDs        []string
	EventIDs         []string
	SourceKind       string
}

type trajectoryEvent struct {
	ID               string
	EventType        string
	SessionID        string
	TaskID           string
	Subtask          string
	Outcome          string
	Summary          string
	FailureSignature string
	RelatedFiles     []string
	CreatedAt        time.Time
}

type subtaskSummary struct {
	Name              string
	Summary           string
	Outcome           string
	Failed            bool
	Succeeded         bool
	FailureSignatures []string
	RelatedFiles      []string
	Repetitions       int
	SessionIDs        []string
	BundleIDs         []string
	EventIDs          []string
}

func (s *TrajectoryMemoryService) upsertTip(ctx context.Context, candidate tipCandidate) (*models.MemoryTip, error) {
	actionStepsJSON, err := json.Marshal(candidate.ActionSteps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tip action steps: %w", err)
	}
	keywords := uniqueTokens(candidate.Keywords)
	keywordsJSON, err := json.Marshal(keywords)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tip keywords: %w", err)
	}

	var lastRetrievedAt *time.Time
	tip := &models.MemoryTip{}
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO memory_tips (
			id, org_id, repo_full_name, source_branch, tip_type, scope, title, content,
			trigger_condition, action_steps, priority, confidence, canonical_key,
			failure_signature, task_fingerprint, keywords, search_text, semantic_summary, outcome_class,
			embedding_status, embedding_model, embedding_updated_at,
			evidence_count, use_count, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,
			$9,$10,$11,$12,$13,
			$14,$15,$16,$17,$18,$19,
			'pending','',NULL,
			1,0,NOW(),NOW()
		)
		ON CONFLICT (org_id, repo_full_name, canonical_key, tip_type) DO UPDATE SET
			source_branch = EXCLUDED.source_branch,
			scope = EXCLUDED.scope,
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			trigger_condition = EXCLUDED.trigger_condition,
			action_steps = EXCLUDED.action_steps,
			priority = EXCLUDED.priority,
			confidence = GREATEST(memory_tips.confidence, EXCLUDED.confidence),
			failure_signature = EXCLUDED.failure_signature,
			task_fingerprint = EXCLUDED.task_fingerprint,
			keywords = EXCLUDED.keywords,
			search_text = EXCLUDED.search_text,
			semantic_summary = EXCLUDED.semantic_summary,
			outcome_class = EXCLUDED.outcome_class,
			embedding_status = CASE
				WHEN memory_tips.content IS DISTINCT FROM EXCLUDED.content
				  OR memory_tips.semantic_summary IS DISTINCT FROM EXCLUDED.semantic_summary
				THEN 'pending'
				ELSE memory_tips.embedding_status
			END,
			evidence_count = memory_tips.evidence_count + 1,
			updated_at = NOW()
		RETURNING id, org_id, repo_full_name, source_branch, tip_type, scope, title, content,
		          trigger_condition, action_steps, priority, confidence, canonical_key,
		          failure_signature, task_fingerprint, keywords, search_text, semantic_summary, outcome_class,
		          embedding_status, embedding_model, embedding_updated_at,
		          evidence_count, use_count, last_retrieved_at, created_at, updated_at
	`,
		uuid.New().String(),
		candidate.OrgID,
		candidate.RepoFullName,
		candidate.SourceBranch,
		candidate.TipType,
		valueOrDefault(candidate.Scope, "task"),
		candidate.Title,
		candidate.Content,
		candidate.TriggerCondition,
		actionStepsJSON,
		valueOrDefault(candidate.Priority, "medium"),
		clampConfidence(candidate.Confidence),
		candidate.CanonicalKey,
		candidate.FailureSignature,
		candidate.TaskFingerprint,
		keywordsJSON,
		candidate.SearchText,
		firstNonEmpty(candidate.SemanticSummary, candidate.Content),
		candidate.OutcomeClass,
	).Scan(
		&tip.ID, &tip.OrgID, &tip.RepoFullName, &tip.SourceBranch, &tip.TipType, &tip.Scope,
		&tip.Title, &tip.Content, &tip.TriggerCondition, &actionStepsJSON, &tip.Priority,
		&tip.Confidence, &tip.CanonicalKey, &tip.FailureSignature, &tip.TaskFingerprint,
		&keywordsJSON, &tip.SearchText, &tip.SemanticSummary, &tip.OutcomeClass,
		&tip.EmbeddingStatus, &tip.EmbeddingModel, &tip.EmbeddingUpdatedAt,
		&tip.EvidenceCount, &tip.UseCount, &lastRetrievedAt, &tip.CreatedAt, &tip.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert memory tip: %w", err)
	}
	tip.LastRetrievedAt = lastRetrievedAt
	_ = json.Unmarshal(actionStepsJSON, &tip.ActionSteps)
	_ = json.Unmarshal(keywordsJSON, &tip.Keywords)

	if candidate.TaskID != "" || len(candidate.SessionIDs) > 0 || len(candidate.BundleIDs) > 0 || len(candidate.EventIDs) > 0 {
		if err := s.addTipSources(ctx, tip.ID, candidate); err != nil {
			return nil, err
		}
	}

	return tip, nil
}

func (s *TrajectoryMemoryService) addTipSources(ctx context.Context, tipID string, candidate tipCandidate) error {
	sourceKind := valueOrDefault(candidate.SourceKind, "trajectory")
	sessionIDs := firstNStrings(uniqueStrings(candidate.SessionIDs), 4)
	bundleIDs := firstNStrings(uniqueStrings(candidate.BundleIDs), 6)
	eventIDs := firstNStrings(uniqueStrings(candidate.EventIDs), 6)

	if len(sessionIDs) == 0 && len(bundleIDs) == 0 && len(eventIDs) == 0 {
		_, err := s.db.Pool.Exec(ctx, `
			INSERT INTO memory_tip_sources (id, tip_id, task_id, source_kind, created_at)
			VALUES ($1,$2,$3,$4,NOW())
		`, uuid.New().String(), tipID, nullableString(candidate.TaskID), sourceKind)
		return err
	}

	for _, sessionID := range append(sessionIDs, "") {
		if sessionID == "" && len(sessionIDs) > 0 {
			break
		}
		if len(bundleIDs) == 0 && len(eventIDs) == 0 {
			_, err := s.db.Pool.Exec(ctx, `
				INSERT INTO memory_tip_sources (id, tip_id, task_id, session_id, source_kind, created_at)
				VALUES ($1,$2,$3,$4,$5,NOW())
			`, uuid.New().String(), tipID, nullableString(candidate.TaskID), nullableString(sessionID), sourceKind)
			if err != nil {
				return fmt.Errorf("failed to insert tip source: %w", err)
			}
			continue
		}
		for _, bundleID := range append(bundleIDs, "") {
			for _, eventID := range append(eventIDs, "") {
				if bundleID == "" && len(bundleIDs) > 0 && eventID == "" && len(eventIDs) > 0 {
					continue
				}
				_, err := s.db.Pool.Exec(ctx, `
					INSERT INTO memory_tip_sources (id, tip_id, task_id, session_id, bundle_id, event_id, source_kind, created_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
				`, uuid.New().String(), tipID, nullableString(candidate.TaskID), nullableString(sessionID), nullableString(bundleID), nullableString(eventID), sourceKind)
				if err != nil {
					return fmt.Errorf("failed to insert tip source: %w", err)
				}
			}
		}
	}

	return nil
}

func (s *TrajectoryMemoryService) recordRetrieval(ctx context.Context, tipID, taskID, sessionID string, score float64, reason string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO memory_tip_retrievals (id, tip_id, task_id, session_id, score, reason, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW())
	`, uuid.New().String(), tipID, nullableString(taskID), nullableString(sessionID), score, reason)
	if err != nil {
		return fmt.Errorf("failed to insert memory tip retrieval: %w", err)
	}

	_, err = s.db.Pool.Exec(ctx, `
		UPDATE memory_tips
		SET use_count = use_count + 1, last_retrieved_at = NOW(), updated_at = updated_at
		WHERE id = $1
	`, tipID)
	return err
}

func (s *TrajectoryMemoryService) getTask(ctx context.Context, orgID, taskID string) (*models.AgentTask, error) {
	task := &models.AgentTask{}
	var outputJSON []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, COALESCE(parent_task_id, ''), COALESCE(linear_issue_id, ''),
		       COALESCE(linear_identifier, ''), COALESCE(linear_url, ''), title,
		       COALESCE(description, ''), COALESCE(prompt, ''), COALESCE(environment_id, ''),
		       COALESCE(branch, ''), COALESCE(repo_full_name, ''), status,
		       COALESCE(output_summary, ''), COALESCE(output_json, '{}'::jsonb), COALESCE(commit_sha, ''),
		       COALESCE(pr_url, ''), COALESCE(error_message, ''), started_at, completed_at,
		       COALESCE(duration_seconds, 0), COALESCE(tokens_used, 0), COALESCE(estimated_cost, 0),
		       COALESCE(retry_count, 0), COALESCE(max_retries, 0), COALESCE(context_saved, false),
		       COALESCE(snapshot_taken, false), created_at, updated_at
		FROM agent_tasks WHERE id = $1 AND org_id = $2
	`, taskID, orgID).Scan(
		&task.ID, &task.OrgID, &task.ParentTaskID, &task.LinearIssueID, &task.LinearIdentifier,
		&task.LinearURL, &task.Title, &task.Description, &task.Prompt, &task.EnvironmentID,
		&task.Branch, &task.RepoFullName, &task.Status, &task.OutputSummary, &outputJSON,
		&task.CommitSHA, &task.PRURL, &task.ErrorMessage, &task.StartedAt, &task.CompletedAt,
		&task.DurationSeconds, &task.TokensUsed, &task.EstimatedCost, &task.RetryCount,
		&task.MaxRetries, &task.ContextSaved, &task.SnapshotTaken, &task.CreatedAt, &task.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	_ = json.Unmarshal(outputJSON, &task.OutputJSON)
	return task, nil
}

func (s *TrajectoryMemoryService) getTaskLogs(ctx context.Context, taskID string) ([]*models.TaskLogEntry, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, task_id, step, status, COALESCE(message, ''), metadata, created_at
		FROM task_execution_log
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query task logs: %w", err)
	}
	defer rows.Close()

	var logs []*models.TaskLogEntry
	for rows.Next() {
		entry := &models.TaskLogEntry{}
		var metadataJSON []byte
		if err := rows.Scan(&entry.ID, &entry.TaskID, &entry.Step, &entry.Status, &entry.Message, &metadataJSON, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task log: %w", err)
		}
		_ = json.Unmarshal(metadataJSON, &entry.Metadata)
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating task logs: %w", err)
	}
	return logs, nil
}

func latestAttemptLogs(task *models.AgentTask, logs []*models.TaskLogEntry) []*models.TaskLogEntry {
	if len(logs) == 0 {
		return logs
	}

	var boundary *time.Time
	for _, log := range logs {
		if log == nil || log.Step != "execution_started" {
			continue
		}
		if boundary == nil || log.CreatedAt.After(*boundary) {
			ts := log.CreatedAt
			boundary = &ts
		}
	}
	if boundary == nil && task != nil && task.StartedAt != nil {
		ts := *task.StartedAt
		boundary = &ts
	}
	if boundary == nil {
		return logs
	}

	filtered := make([]*models.TaskLogEntry, 0, len(logs))
	for _, log := range logs {
		if log == nil {
			continue
		}
		if log.CreatedAt.Before(*boundary) {
			continue
		}
		filtered = append(filtered, log)
	}
	if len(filtered) == 0 {
		return logs
	}
	return filtered
}

func latestAttemptSessions(task *models.AgentTask, logs []*models.TaskLogEntry, sessions []*models.AgentSession) []*models.AgentSession {
	if len(sessions) == 0 {
		return sessions
	}

	var boundary *time.Time
	if filteredLogs := latestAttemptLogs(task, logs); len(filteredLogs) > 0 {
		ts := filteredLogs[0].CreatedAt
		boundary = &ts
	}
	if boundary == nil && task != nil && task.StartedAt != nil {
		ts := *task.StartedAt
		boundary = &ts
	}
	if boundary == nil {
		return sessions
	}

	filtered := make([]*models.AgentSession, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if session.CreatedAt.Before(*boundary) {
			continue
		}
		filtered = append(filtered, session)
	}
	if len(filtered) == 0 {
		return []*models.AgentSession{sessions[len(sessions)-1]}
	}
	return filtered
}

func (s *TrajectoryMemoryService) getTrajectoryEvents(ctx context.Context, task *models.AgentTask, sessionIDs []string) ([]trajectoryEvent, error) {
	if task == nil || task.OrgID == "" || task.Branch == "" || task.RepoFullName == "" {
		return nil, nil
	}

	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, event_type, data, created_at
		FROM context_events
		WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3
		ORDER BY created_at ASC
	`, task.OrgID, task.RepoFullName, task.Branch)
	if err != nil {
		return nil, fmt.Errorf("failed to query context events: %w", err)
	}
	defer rows.Close()

	sessionSet := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		sessionSet[id] = true
	}

	var items []trajectoryEvent
	for rows.Next() {
		var (
			id        string
			eventType string
			dataJSON  []byte
			createdAt time.Time
		)
		if err := rows.Scan(&id, &eventType, &dataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan context event: %w", err)
		}

		var raw map[string]interface{}
		if err := json.Unmarshal(dataJSON, &raw); err != nil {
			continue
		}

		taskID := stringValue(raw["task_id"])
		sessionID := stringValue(raw["session_id"])
		if taskID != "" && taskID != task.ID {
			continue
		}
		if taskID == "" && sessionID != "" && !sessionSet[sessionID] {
			continue
		}
		if taskID == "" && sessionID == "" && task.StartedAt != nil && createdAt.Before(task.StartedAt.Add(-2*time.Minute)) {
			continue
		}
		if task.CompletedAt != nil && createdAt.After(task.CompletedAt.Add(2*time.Minute)) {
			continue
		}

		relatedFiles := stringSliceValue(raw["related_files"])
		items = append(items, trajectoryEvent{
			ID:               id,
			EventType:        eventType,
			SessionID:        sessionID,
			TaskID:           taskID,
			Subtask:          stringValue(raw["subtask"]),
			Outcome:          stringValue(raw["outcome"]),
			Summary:          firstNonEmpty(stringValue(raw["message"]), stringValue(raw["summary"])),
			FailureSignature: normalizedKey(firstNonEmpty(stringValue(raw["failure_signature"]), stringValue(raw["error"]))),
			RelatedFiles:     relatedFiles,
			CreatedAt:        createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating context events: %w", err)
	}

	return items, nil
}

func collectSubtasks(task *models.AgentTask, logs []*models.TaskLogEntry, bundles []*models.ChangeBundle, events []trajectoryEvent) map[string]*subtaskSummary {
	result := map[string]*subtaskSummary{}
	ensure := func(name string) *subtaskSummary {
		name = strings.TrimSpace(name)
		if name == "" {
			name = "task"
		}
		key := normalizedKey(name)
		st, ok := result[key]
		if !ok {
			st = &subtaskSummary{Name: name}
			result[key] = st
		}
		return st
	}

	for _, event := range events {
		st := ensure(firstNonEmpty(event.Subtask, event.EventType))
		st.Repetitions++
		if event.Outcome == "failed" {
			st.Failed = true
			st.Outcome = "failed"
		}
		if event.Outcome == "completed" || event.Outcome == "success" {
			st.Succeeded = true
			if !st.Failed {
				st.Outcome = "completed"
			}
		}
		if event.FailureSignature != "" {
			st.FailureSignatures = append(st.FailureSignatures, event.FailureSignature)
		}
		if event.Summary != "" {
			st.Summary = event.Summary
		}
		st.RelatedFiles = append(st.RelatedFiles, event.RelatedFiles...)
		if event.SessionID != "" {
			st.SessionIDs = append(st.SessionIDs, event.SessionID)
		}
		st.EventIDs = append(st.EventIDs, event.ID)
	}

	for _, bundle := range bundles {
		name := stringValue(bundle.DecisionDiff["subtask"])
		if name == "" {
			name = stringValue(bundle.DecisionDiff["event"])
		}
		if name == "" {
			name = stringValue(bundle.ContextDiff["subtask"])
		}
		if name == "" {
			continue
		}
		st := ensure(name)
		st.Repetitions++
		st.BundleIDs = append(st.BundleIDs, bundle.ID)
		st.SessionIDs = append(st.SessionIDs, bundle.SessionID)
		if summary := firstNonEmpty(stringValue(bundle.DecisionDiff["summary"]), stringValue(bundle.ContextDiff["summary"])); summary != "" {
			st.Summary = summary
		}
		outcome := strings.ToLower(firstNonEmpty(stringValue(bundle.DecisionDiff["outcome"]), stringValue(bundle.ContextDiff["outcome"])))
		switch outcome {
		case "failed":
			st.Failed = true
			st.Outcome = "failed"
		case "completed", "success":
			st.Succeeded = true
			if !st.Failed {
				st.Outcome = "completed"
			}
		}
		if failureSig := normalizedKey(firstNonEmpty(stringValue(bundle.DecisionDiff["failure_signature"]), stringValue(bundle.ContextDiff["failure_signature"]))); failureSig != "" {
			st.FailureSignatures = append(st.FailureSignatures, failureSig)
		}
		st.RelatedFiles = append(st.RelatedFiles, stringSliceValue(bundle.DecisionDiff["related_files"])...)
	}

	for _, logEntry := range logs {
		step := strings.TrimSpace(logEntry.Step)
		if step == "" {
			continue
		}
		if !strings.HasPrefix(step, "subtask_") && step != "claude_done" && step != "completed" && step != "failed" {
			continue
		}
		name := strings.TrimPrefix(step, "subtask_")
		st := ensure(name)
		st.Repetitions++
		if logEntry.Status == "failed" || step == "failed" {
			st.Failed = true
			st.Outcome = "failed"
			if logEntry.Message != "" {
				st.FailureSignatures = append(st.FailureSignatures, normalizedKey(logEntry.Message))
			}
		}
		if logEntry.Status == "completed" || step == "completed" || step == "claude_done" {
			st.Succeeded = true
			if !st.Failed {
				st.Outcome = "completed"
			}
		}
		if st.Summary == "" && logEntry.Message != "" {
			st.Summary = logEntry.Message
		}
	}

	if len(result) == 0 {
		st := ensure("task")
		st.Summary = firstNonEmpty(task.OutputSummary, task.Description, task.Title)
		if task.Status == "complete" {
			st.Succeeded = true
			st.Outcome = "completed"
		}
		if task.Status == "failed" {
			st.Failed = true
			st.Outcome = "failed"
			st.FailureSignatures = append(st.FailureSignatures, normalizedKey(task.ErrorMessage))
		}
	}

	for _, st := range result {
		st.RelatedFiles = uniqueStrings(st.RelatedFiles)
		st.SessionIDs = uniqueStrings(st.SessionIDs)
		st.BundleIDs = uniqueStrings(st.BundleIDs)
		st.EventIDs = uniqueStrings(st.EventIDs)
		st.FailureSignatures = uniqueStrings(st.FailureSignatures)
	}

	return result
}

func trajectoryHasFailure(task *models.AgentTask, logs []*models.TaskLogEntry, subtasks map[string]*subtaskSummary) bool {
	if task != nil && task.Status == "failed" {
		return true
	}
	for _, logEntry := range logs {
		if logEntry.Status == "failed" || logEntry.Step == "failed" {
			return true
		}
	}
	for _, st := range subtasks {
		if st.Failed {
			return true
		}
	}
	return false
}

func trajectoryHasRecovery(subtasks map[string]*subtaskSummary) bool {
	for _, st := range subtasks {
		if st.Failed && st.Succeeded {
			return true
		}
	}
	return false
}

func buildRecoveryContent(st *subtaskSummary) string {
	base := fmt.Sprintf("When `%s` fails, do not blindly retry. Use the previously successful recovery path: inspect the failing precondition, fix it, then rerun the subtask.", st.displayName())
	if st.Summary != "" {
		base = fmt.Sprintf("When `%s` fails, use the previously successful recovery path captured in this trajectory: %s", st.displayName(), st.Summary)
	}
	if sig := st.failureSignature(); sig != "" {
		base += fmt.Sprintf(" The repeating failure signature was `%s`.", sig)
	}
	return base
}

func buildStrategyContent(st *subtaskSummary, task *models.AgentTask) string {
	if st.Summary != "" {
		return fmt.Sprintf("For `%s`, reuse this validated approach: %s", st.displayName(), st.Summary)
	}
	return fmt.Sprintf("For tasks similar to `%s`, keep `%s` scoped, verify the changed area, and only then move to the next step.", task.Title, st.displayName())
}

func buildOptimizationContent(st *subtaskSummary) string {
	return fmt.Sprintf("`%s` required %d passes in a successful run. Reduce repetition by checking prerequisites and batching related edits before rerunning the same loop.", st.displayName(), st.Repetitions)
}

func recoverySteps(st *subtaskSummary) []string {
	steps := []string{
		fmt.Sprintf("Inspect the failing condition for `%s` before retrying.", st.displayName()),
		"Apply the corrective action to the root cause, not just the symptom.",
		fmt.Sprintf("Re-run `%s` and capture the updated outcome.", st.displayName()),
	}
	if len(st.RelatedFiles) > 0 {
		steps[0] = fmt.Sprintf("Inspect the failing condition in %s before retrying `%s`.", strings.Join(firstNStrings(st.RelatedFiles, 3), ", "), st.displayName())
	}
	return steps
}

func strategySteps(st *subtaskSummary) []string {
	steps := []string{
		fmt.Sprintf("Start `%s` by reading the existing implementation path.", st.displayName()),
		"Make the scoped change and verify the immediate result.",
		"Record the completed subtask before moving on.",
	}
	if len(st.RelatedFiles) > 0 {
		steps[0] = fmt.Sprintf("Start `%s` by reading %s.", st.displayName(), strings.Join(firstNStrings(st.RelatedFiles, 3), ", "))
	}
	return steps
}

func optimizationSteps(st *subtaskSummary) []string {
	return []string{
		fmt.Sprintf("Before repeating `%s`, confirm the prerequisite that caused the extra pass.", st.displayName()),
		"Batch closely related edits before rerunning the same validation loop.",
		"Prefer targeted verification first, then run broader checks once the local issue is resolved.",
	}
}

func dedupeCandidates(candidates []tipCandidate) []tipCandidate {
	seen := map[string]tipCandidate{}
	for _, candidate := range candidates {
		key := candidate.TipType + "|" + candidate.CanonicalKey
		if existing, ok := seen[key]; ok {
			if candidate.Confidence > existing.Confidence {
				seen[key] = candidate
			}
			continue
		}
		seen[key] = candidate
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]tipCandidate, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result
}

func scoreTip(req MemoryRetrieveRequest, requestTokens []string, tip *models.MemoryTip) (float64, string) {
	score := 0.0
	reasons := make([]string, 0, 4)

	if tip.SourceBranch != "" && tip.SourceBranch == req.Branch {
		score += 1.5
		reasons = append(reasons, "same branch")
	}

	if req.FailureSignature != "" && tip.FailureSignature != "" {
		if normalizedKey(req.FailureSignature) == normalizedKey(tip.FailureSignature) {
			score += 6
			reasons = append(reasons, "failure signature match")
		} else if strings.Contains(tip.FailureSignature, normalizedKey(req.FailureSignature)) || strings.Contains(normalizedKey(req.FailureSignature), tip.FailureSignature) {
			score += 3
			reasons = append(reasons, "similar failure signature")
		}
	}

	if req.Subtask != "" {
		subtaskOverlap := tokenOverlap(uniqueTokens(tokenize(req.Subtask)), tip.Keywords)
		if subtaskOverlap > 0 {
			score += 2.5 * subtaskOverlap
			reasons = append(reasons, "subtask overlap")
		}
	}

	searchTokens := uniqueTokens(append(tokenize(tip.SearchText), tip.Keywords...))
	lexical := tokenOverlap(requestTokens, searchTokens)
	if lexical > 0 {
		score += lexical * 7
		reasons = append(reasons, "task similarity")
	}

	score += priorityWeight(tip.Priority) * 0.35
	score += float64(minInt(tip.EvidenceCount, 5)) * 0.15

	if req.FailureSignature != "" && tip.TipType == "recovery" {
		score += 0.75
	}
	if req.FailureSignature == "" && tip.TipType == "strategy" {
		score += 0.4
	}
	if tip.TipType == "optimization" && strings.Contains(strings.ToLower(req.TaskPrompt), "repeat") {
		score += 0.75
	}

	if lexical == 0 && req.FailureSignature == "" && req.Subtask == "" && tip.SourceBranch != req.Branch {
		return 0, ""
	}

	return score, strings.Join(reasons, ", ")
}

func scanMemoryTip(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.MemoryTip, error) {
	tip := &models.MemoryTip{}
	var (
		actionStepsJSON []byte
		keywordsJSON    []byte
		lastRetrievedAt *time.Time
	)
	err := scanner.Scan(
		&tip.ID, &tip.OrgID, &tip.RepoFullName, &tip.SourceBranch, &tip.TipType, &tip.Scope,
		&tip.Title, &tip.Content, &tip.TriggerCondition, &actionStepsJSON, &tip.Priority,
		&tip.Confidence, &tip.CanonicalKey, &tip.FailureSignature, &tip.TaskFingerprint,
		&keywordsJSON, &tip.SearchText, &tip.SemanticSummary, &tip.OutcomeClass,
		&tip.EmbeddingStatus, &tip.EmbeddingModel, &tip.EmbeddingUpdatedAt,
		&tip.EvidenceCount, &tip.UseCount, &lastRetrievedAt, &tip.CreatedAt, &tip.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan memory tip: %w", err)
	}
	tip.LastRetrievedAt = lastRetrievedAt
	_ = json.Unmarshal(actionStepsJSON, &tip.ActionSteps)
	_ = json.Unmarshal(keywordsJSON, &tip.Keywords)
	return tip, nil
}

func scanRetrievalRun(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.RetrievalRun, error) {
	run := &models.RetrievalRun{}
	var (
		taskID        string
		sessionID     string
		candidateJSON []byte
		rerankedJSON  []byte
		selectedJSON  []byte
	)
	err := scanner.Scan(
		&run.ID, &run.OrgID, &run.RepoFullName, &taskID, &sessionID,
		&run.QueryText, &run.Subtask, &run.FailureSignature,
		&candidateJSON, &rerankedJSON, &selectedJSON, &run.VectorSearchUsed,
		&run.RerankerModel, &run.Status, &run.LatencyMs, &run.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan retrieval run: %w", err)
	}
	run.TaskID = taskID
	run.SessionID = sessionID
	_ = json.Unmarshal(candidateJSON, &run.CandidateTipIDs)
	_ = json.Unmarshal(rerankedJSON, &run.RerankedTipIDs)
	_ = json.Unmarshal(selectedJSON, &run.SelectedTipIDs)
	return run, nil
}

func (st *subtaskSummary) displayName() string {
	if strings.TrimSpace(st.Name) == "" {
		return "task"
	}
	return st.Name
}

func (st *subtaskSummary) failureSignature() string {
	if len(st.FailureSignatures) == 0 {
		return ""
	}
	return st.FailureSignatures[0]
}

func (st *subtaskSummary) keywords() []string {
	return uniqueTokens(append(tokenize(st.Name), tokenize(st.Summary)...))
}

func (st *subtaskSummary) scope() string {
	if normalizedKey(st.Name) == "task" {
		return "task"
	}
	return "subtask"
}

func normalizedKey(input string) string {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, "-")
}

func normalizeText(input string) string {
	return strings.Join(uniqueTokens(tokenize(input)), " ")
}

func tokenize(input string) []string {
	input = strings.ToLower(input)
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		".", " ",
		",", " ",
		":", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"/", " ",
		"\\", " ",
		"_", " ",
		"-", " ",
		"`", " ",
		"'", " ",
		"\"", " ",
	)
	input = replacer.Replace(input)
	parts := strings.Fields(input)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		switch part {
		case "with", "from", "that", "this", "into", "when", "then", "than", "were", "have", "will", "task", "step", "repo", "branch":
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}

func uniqueTokens(tokens []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		result = append(result, token)
	}
	sort.Strings(result)
	return result
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

func tokenOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setB := map[string]bool{}
	for _, token := range b {
		setB[token] = true
	}
	matches := 0
	for _, token := range uniqueTokens(a) {
		if setB[token] {
			matches++
		}
	}
	denominator := maxInt(len(uniqueTokens(a)), len(uniqueTokens(b)))
	if denominator == 0 {
		return 0
	}
	return float64(matches) / float64(denominator)
}

func priorityWeight(priority string) float64 {
	switch strings.ToLower(priority) {
	case "critical":
		return 4
	case "high":
		return 3
	case "low":
		return 1
	default:
		return 2
	}
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func shouldSkipTrajectoryMemory(task *models.AgentTask) bool {
	if task == nil {
		return true
	}

	text := strings.ToLower(strings.Join([]string{
		task.ErrorMessage,
		task.OutputSummary,
	}, " "))

	skipPatterns := []string{
		"invalid api key",
		"fix external api key",
		"claude code not configured",
		"authentication failed",
		"authentication error",
		"unauthorized",
		"forbidden",
		"api key is invalid",
		"missing api key",
		"no valid session id",
		"--resume requires a valid session id",
		"provider unavailable",
		"failed to provision environment",
		"environment failed to become ready",
		"instance never became",
		"cloud-init did not complete",
		"docker not installed",
		"claude cli install failed",
	}

	for _, pattern := range skipPatterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func stringSliceValue(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(stringValue(item)); s != "" {
				items = append(items, s)
			}
		}
		return items
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNStrings(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *TrajectoryMemoryService) hasVectorEmbeddingColumn(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'memory_tip_embeddings' AND column_name = 'embedding_vector'
		)
	`).Scan(&exists)
	return exists, err
}

func (s *TrajectoryMemoryService) upsertVectorEmbedding(ctx context.Context, tipID string, embedding Embedding) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO memory_tip_embeddings (id, tip_id, provider, model, dimensions, embedding_vector, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6::vector,NOW(),NOW())
		ON CONFLICT (tip_id, provider, model) DO UPDATE SET
			dimensions = EXCLUDED.dimensions,
			embedding_vector = EXCLUDED.embedding_vector,
			updated_at = NOW()
	`, uuid.New().String(), tipID, s.embeddingProvider.ProviderName(), s.embeddingProvider.ModelName(), embedding.Dimensions, vectorLiteral(embedding.Values))
	return err
}

func (s *TrajectoryMemoryService) upsertJSONEmbedding(ctx context.Context, tipID string, embedding Embedding) error {
	payload, _ := json.Marshal(embedding.Values)
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO memory_tip_embeddings (id, tip_id, provider, model, dimensions, embedding_vector_json, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW())
		ON CONFLICT (tip_id, provider, model) DO UPDATE SET
			dimensions = EXCLUDED.dimensions,
			embedding_vector_json = EXCLUDED.embedding_vector_json,
			updated_at = NOW()
	`, uuid.New().String(), tipID, s.embeddingProvider.ProviderName(), s.embeddingProvider.ModelName(), embedding.Dimensions, payload)
	return err
}

func vectorLiteral(values []float32) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%f", value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
