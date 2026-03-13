package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

const trajectoryAnalyzerVersion = "v2"

type TrajectoryAnalysisService struct {
	db             *db.DB
	claudeService  *ClaudeService
	sessionService *SessionService
}

func NewTrajectoryAnalysisService(database *db.DB, claudeService *ClaudeService, sessionService *SessionService) *TrajectoryAnalysisService {
	return &TrajectoryAnalysisService{
		db:             database,
		claudeService:  claudeService,
		sessionService: sessionService,
	}
}

type normalizedTrajectory struct {
	Task     *models.AgentTask          `json:"task"`
	Logs     []*models.TaskLogEntry     `json:"logs"`
	Sessions []*models.AgentSession     `json:"sessions"`
	Bundles  []*models.ChangeBundle     `json:"bundles"`
	Events   []trajectoryEvent          `json:"events"`
	Subtasks map[string]*subtaskSummary `json:"subtasks"`
	Summary  string                     `json:"summary"`
}

func (s *TrajectoryAnalysisService) AnalyzeTask(ctx context.Context, orgID, taskID string) (*models.TrajectoryAnalysis, error) {
	existing, err := s.GetLatestAnalysis(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	traj, err := s.buildTrajectory(ctx, orgID, taskID)
	if err != nil {
		return nil, err
	}
	if traj == nil || traj.Task == nil {
		return nil, nil
	}

	analysis := s.heuristicAnalysis(traj)
	analysis.AnalyzerVersion = trajectoryAnalyzerVersion
	analysis.ModelName = "heuristic"

	if s.claudeService != nil {
		prompt := s.buildAnalysisPrompt(traj, analysis)
		if text, model, callErr := s.claudeService.Complete(ctx, orgID, prompt, 2400); callErr == nil {
			if llmAnalysis, parseErr := parseTrajectoryAnalysisResponse(text); parseErr == nil {
				analysis = llmAnalysis
				analysis.ModelName = model
				analysis.AnalyzerVersion = trajectoryAnalyzerVersion
				if analysis.Confidence == 0 {
					analysis.Confidence = 0.78
				}
			}
		}
	}

	analysis.ID = uuid.New().String()
	analysis.OrgID = traj.Task.OrgID
	analysis.RepoFullName = traj.Task.RepoFullName
	analysis.TaskID = traj.Task.ID
	analysis.SourceBranch = traj.Task.Branch
	analysis.TrajectorySummary = firstNonEmpty(analysis.TrajectorySummary, traj.Summary, traj.Task.OutputSummary, traj.Task.Description, traj.Task.Title)
	if len(traj.Sessions) > 0 {
		analysis.SessionID = traj.Sessions[len(traj.Sessions)-1].ID
	}

	if err := s.upsertAnalysis(ctx, analysis); err != nil {
		return nil, err
	}
	return analysis, nil
}

func (s *TrajectoryAnalysisService) GetLatestAnalysis(ctx context.Context, taskID string) (*models.TrajectoryAnalysis, error) {
	row := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, repo_full_name, task_id, COALESCE(session_id::text, ''), COALESCE(source_branch, ''),
		       COALESCE(trajectory_summary, ''), COALESCE(outcome_class, ''), COALESCE(immediate_cause, ''),
		       COALESCE(proximate_cause, ''), COALESCE(root_cause, ''), COALESCE(recovery_action, ''),
		       COALESCE(recovery_reason, ''), COALESCE(inefficiency_pattern, ''), COALESCE(recommended_alternative, ''),
		       subtask_analyses, COALESCE(analyzer_version, ''), COALESCE(model_name, ''), confidence,
		       created_at, updated_at
		FROM trajectory_analyses
		WHERE task_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID)
	analysis, err := scanTrajectoryAnalysis(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return analysis, err
}

func (s *TrajectoryAnalysisService) buildTrajectory(ctx context.Context, orgID, taskID string) (*normalizedTrajectory, error) {
	mem := &TrajectoryMemoryService{db: s.db, sessionService: s.sessionService}
	task, err := mem.getTask(ctx, orgID, taskID)
	if err != nil || task == nil {
		return nil, err
	}
	logs, err := mem.getTaskLogs(ctx, taskID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.sessionService.ListSessionsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	sessionIDs := make([]string, 0, len(sessions))
	var bundles []*models.ChangeBundle
	for _, session := range sessions {
		sessionIDs = append(sessionIDs, session.ID)
		items, err := s.sessionService.ListBundlesBySession(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, items...)
	}
	events, err := mem.getTrajectoryEvents(ctx, task, sessionIDs)
	if err != nil {
		return nil, err
	}
	subtasks := collectSubtasks(task, logs, bundles, events)

	var summaryParts []string
	summaryParts = append(summaryParts, firstNonEmpty(task.OutputSummary, task.Description, task.Title))
	for _, st := range subtasks {
		if st.Summary != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("%s: %s", st.displayName(), st.Summary))
		}
	}

	return &normalizedTrajectory{
		Task:     task,
		Logs:     logs,
		Sessions: sessions,
		Bundles:  bundles,
		Events:   events,
		Subtasks: subtasks,
		Summary:  strings.Join(uniqueStrings(summaryParts), " | "),
	}, nil
}

func (s *TrajectoryAnalysisService) heuristicAnalysis(traj *normalizedTrajectory) *models.TrajectoryAnalysis {
	analysis := &models.TrajectoryAnalysis{
		OutcomeClass: "clean_success",
		Confidence:   0.58,
	}
	if traj == nil || traj.Task == nil {
		return analysis
	}

	hasFailure := trajectoryHasFailure(traj.Task, traj.Logs, traj.Subtasks)
	hasRecovery := trajectoryHasRecovery(traj.Subtasks)
	inefficient := false
	subtaskAnalyses := make([]models.TrajectorySubtaskAnalysis, 0, len(traj.Subtasks))
	for _, st := range traj.Subtasks {
		item := models.TrajectorySubtaskAnalysis{
			Name:             st.displayName(),
			Summary:          st.Summary,
			FailureSignature: st.failureSignature(),
			RelatedFiles:     st.RelatedFiles,
		}
		switch {
		case st.Failed && st.Succeeded:
			item.OutcomeClass = "recovered"
			item.ImmediateCause = firstNonEmpty(st.failureSignature(), st.Summary, "subtask failed before corrective action")
			item.RootCause = firstNonEmpty(st.failureSignature(), "missing prerequisite or incorrect local assumption")
			item.RecoveryAction = firstNonEmpty(st.Summary, "apply the corrective action before retrying")
			item.RecoveryReason = "the follow-up attempt succeeded after the failing condition changed"
		case st.Failed:
			item.OutcomeClass = "failure"
			item.ImmediateCause = firstNonEmpty(st.failureSignature(), st.Summary, "subtask failed")
			item.RootCause = firstNonEmpty(st.failureSignature(), "the task did not satisfy a required prerequisite")
		case st.Repetitions >= 2 && st.Succeeded:
			item.OutcomeClass = "inefficient_success"
			item.InefficiencyPattern = fmt.Sprintf("%s required %d passes", st.displayName(), st.Repetitions)
			item.RecommendedAlternative = "batch closely related edits or validate prerequisites before retrying"
			inefficient = true
		default:
			item.OutcomeClass = "clean_success"
			item.RecoveryReason = ""
		}
		item.Actions = extractSubtaskActions(traj.Bundles, st)
		subtaskAnalyses = append(subtaskAnalyses, item)
	}
	analysis.SubtaskAnalyses = subtaskAnalyses

	switch {
	case hasRecovery:
		analysis.OutcomeClass = "recovered"
		analysis.Confidence = 0.72
	case traj.Task.Status == "failed" || hasFailure:
		analysis.OutcomeClass = "failure"
		analysis.Confidence = 0.65
	case inefficient:
		analysis.OutcomeClass = "inefficient_success"
		analysis.Confidence = 0.62
	default:
		analysis.OutcomeClass = "clean_success"
	}

	analysis.TrajectorySummary = traj.Summary
	analysis.ImmediateCause = firstNonEmpty(traj.Task.ErrorMessage, firstFailureSignature(traj.Subtasks), "task outcome matched observed telemetry")
	analysis.ProximateCause = proximateCauseFromSubtasks(traj.Subtasks)
	analysis.RootCause = rootCauseFromSubtasks(traj.Subtasks, analysis.OutcomeClass)
	analysis.RecoveryAction = recoveryActionFromSubtasks(traj.Subtasks)
	analysis.RecoveryReason = recoveryReasonFromSubtasks(traj.Subtasks)
	analysis.InefficiencyPattern = inefficiencyPatternFromSubtasks(traj.Subtasks)
	analysis.RecommendedAlternative = recommendedAlternativeFromSubtasks(traj.Subtasks)
	return analysis
}

func (s *TrajectoryAnalysisService) buildAnalysisPrompt(traj *normalizedTrajectory, fallback *models.TrajectoryAnalysis) string {
	type promptSubtask struct {
		Name              string   `json:"name"`
		Summary           string   `json:"summary"`
		Outcome           string   `json:"outcome"`
		Failed            bool     `json:"failed"`
		Succeeded         bool     `json:"succeeded"`
		FailureSignatures []string `json:"failure_signatures,omitempty"`
		RelatedFiles      []string `json:"related_files,omitempty"`
		Repetitions       int      `json:"repetitions"`
	}

	subtasks := make([]promptSubtask, 0, len(traj.Subtasks))
	for _, st := range traj.Subtasks {
		subtasks = append(subtasks, promptSubtask{
			Name:              st.displayName(),
			Summary:           st.Summary,
			Outcome:           st.Outcome,
			Failed:            st.Failed,
			Succeeded:         st.Succeeded,
			FailureSignatures: st.FailureSignatures,
			RelatedFiles:      st.RelatedFiles,
			Repetitions:       st.Repetitions,
		})
	}

	payload := map[string]interface{}{
		"task": map[string]interface{}{
			"id":             traj.Task.ID,
			"title":          traj.Task.Title,
			"description":    traj.Task.Description,
			"prompt":         traj.Task.Prompt,
			"status":         traj.Task.Status,
			"error_message":  traj.Task.ErrorMessage,
			"output_summary": traj.Task.OutputSummary,
			"branch":         traj.Task.Branch,
			"repo_full_name": traj.Task.RepoFullName,
		},
		"logs":     traj.Logs,
		"subtasks": subtasks,
		"fallback": fallback,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")

	return fmt.Sprintf(`You are analyzing an LLM coding-agent execution trajectory.

Only use observable telemetry and explicit self-reports from the data below. Do not invent hidden reasoning.
Return ONLY valid JSON with this schema:
{
  "trajectory_summary": "short summary",
  "outcome_class": "clean_success|failure|recovered|inefficient_success",
  "immediate_cause": "",
  "proximate_cause": "",
  "root_cause": "",
  "recovery_action": "",
  "recovery_reason": "",
  "inefficiency_pattern": "",
  "recommended_alternative": "",
  "confidence": 0.0,
  "subtask_analyses": [
    {
      "name": "",
      "outcome_class": "clean_success|failure|recovered|inefficient_success",
      "summary": "",
      "immediate_cause": "",
      "proximate_cause": "",
      "root_cause": "",
      "recovery_action": "",
      "recovery_reason": "",
      "inefficiency_pattern": "",
      "recommended_alternative": "",
      "failure_signature": "",
      "related_files": [],
      "actions": []
    }
  ]
}

Prefer specific, attributable causes grounded in failures, retries, repeated loops, bundle summaries, and explicit subtask markers.

Trajectory:
%s`, string(data))
}

func (s *TrajectoryAnalysisService) upsertAnalysis(ctx context.Context, analysis *models.TrajectoryAnalysis) error {
	subtaskJSON, err := json.Marshal(analysis.SubtaskAnalyses)
	if err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO trajectory_analyses (
			id, org_id, repo_full_name, task_id, session_id, source_branch, trajectory_summary,
			outcome_class, immediate_cause, proximate_cause, root_cause, recovery_action,
			recovery_reason, inefficiency_pattern, recommended_alternative, subtask_analyses,
			analyzer_version, model_name, confidence, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NOW(),NOW()
		)
		ON CONFLICT (task_id, session_id) DO UPDATE SET
			source_branch = EXCLUDED.source_branch,
			trajectory_summary = EXCLUDED.trajectory_summary,
			outcome_class = EXCLUDED.outcome_class,
			immediate_cause = EXCLUDED.immediate_cause,
			proximate_cause = EXCLUDED.proximate_cause,
			root_cause = EXCLUDED.root_cause,
			recovery_action = EXCLUDED.recovery_action,
			recovery_reason = EXCLUDED.recovery_reason,
			inefficiency_pattern = EXCLUDED.inefficiency_pattern,
			recommended_alternative = EXCLUDED.recommended_alternative,
			subtask_analyses = EXCLUDED.subtask_analyses,
			analyzer_version = EXCLUDED.analyzer_version,
			model_name = EXCLUDED.model_name,
			confidence = EXCLUDED.confidence,
			updated_at = NOW()
	`, analysis.ID, analysis.OrgID, analysis.RepoFullName, analysis.TaskID, nullableString(analysis.SessionID),
		analysis.SourceBranch, analysis.TrajectorySummary, analysis.OutcomeClass, analysis.ImmediateCause,
		analysis.ProximateCause, analysis.RootCause, analysis.RecoveryAction, analysis.RecoveryReason,
		analysis.InefficiencyPattern, analysis.RecommendedAlternative, subtaskJSON,
		analysis.AnalyzerVersion, analysis.ModelName, clampConfidence(analysis.Confidence))
	return err
}

func scanTrajectoryAnalysis(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.TrajectoryAnalysis, error) {
	analysis := &models.TrajectoryAnalysis{}
	var (
		sessionID   string
		subtaskJSON []byte
	)
	err := scanner.Scan(
		&analysis.ID, &analysis.OrgID, &analysis.RepoFullName, &analysis.TaskID, &sessionID, &analysis.SourceBranch,
		&analysis.TrajectorySummary, &analysis.OutcomeClass, &analysis.ImmediateCause, &analysis.ProximateCause,
		&analysis.RootCause, &analysis.RecoveryAction, &analysis.RecoveryReason, &analysis.InefficiencyPattern,
		&analysis.RecommendedAlternative, &subtaskJSON, &analysis.AnalyzerVersion, &analysis.ModelName,
		&analysis.Confidence, &analysis.CreatedAt, &analysis.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	analysis.SessionID = sessionID
	_ = json.Unmarshal(subtaskJSON, &analysis.SubtaskAnalyses)
	return analysis, nil
}

func parseTrajectoryAnalysisResponse(raw string) (*models.TrajectoryAnalysis, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	analysis := &models.TrajectoryAnalysis{}
	if err := json.Unmarshal([]byte(raw), analysis); err != nil {
		return nil, err
	}
	return analysis, nil
}

func extractSubtaskActions(bundles []*models.ChangeBundle, st *subtaskSummary) []string {
	if st == nil {
		return nil
	}
	var actions []string
	for _, bundle := range bundles {
		name := firstNonEmpty(stringValue(bundle.DecisionDiff["subtask"]), stringValue(bundle.DecisionDiff["event"]), stringValue(bundle.ContextDiff["subtask"]))
		if normalizedKey(name) != normalizedKey(st.Name) {
			continue
		}
		if summary := firstNonEmpty(stringValue(bundle.DecisionDiff["summary"]), stringValue(bundle.ContextDiff["summary"])); summary != "" {
			actions = append(actions, summary)
		}
	}
	return firstNStrings(uniqueStrings(actions), 5)
}

func firstFailureSignature(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if sig := st.failureSignature(); sig != "" {
			return sig
		}
	}
	return ""
}

func proximateCauseFromSubtasks(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if st.Failed {
			return firstNonEmpty(st.Summary, st.failureSignature(), "the immediately preceding subtask failed")
		}
	}
	return ""
}

func rootCauseFromSubtasks(subtasks map[string]*subtaskSummary, outcome string) string {
	for _, st := range subtasks {
		if st.Failed {
			return firstNonEmpty(st.failureSignature(), "a prerequisite, assumption, or configuration issue was never validated")
		}
	}
	if outcome == "inefficient_success" {
		return "the execution loop repeated because prerequisites or batching opportunities were not checked early"
	}
	return "the successful path kept the work scoped and validated as it went"
}

func recoveryActionFromSubtasks(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if st.Failed && st.Succeeded {
			return firstNonEmpty(st.Summary, "the agent corrected the failing condition and retried")
		}
	}
	return ""
}

func recoveryReasonFromSubtasks(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if st.Failed && st.Succeeded {
			return "the repeated attempt succeeded after the subtask state changed"
		}
	}
	return ""
}

func inefficiencyPatternFromSubtasks(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if st.Repetitions >= 2 && st.Succeeded {
			return fmt.Sprintf("%s repeated %d times", st.displayName(), st.Repetitions)
		}
	}
	return ""
}

func recommendedAlternativeFromSubtasks(subtasks map[string]*subtaskSummary) string {
	for _, st := range subtasks {
		if st.Repetitions >= 2 && st.Succeeded {
			return "validate prerequisites earlier and batch related changes before rerunning the same subtask"
		}
	}
	return ""
}
