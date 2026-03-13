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
)

type MemoryRetrievalService struct {
	db                *db.DB
	claudeService     *ClaudeService
	embeddingProvider EmbeddingProvider
}

func NewMemoryRetrievalService(database *db.DB, claudeService *ClaudeService, embeddingProvider EmbeddingProvider) *MemoryRetrievalService {
	if embeddingProvider == nil {
		embeddingProvider = NewNullEmbeddingProvider()
	}
	return &MemoryRetrievalService{
		db:                database,
		claudeService:     claudeService,
		embeddingProvider: embeddingProvider,
	}
}

func (s *MemoryRetrievalService) RetrieveGuidance(ctx context.Context, req MemoryRetrieveRequest) ([]RetrievedTip, error) {
	start := time.Now()
	if req.OrgID == "" || req.RepoFullName == "" {
		return nil, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 7 {
		limit = 7
	}

	candidates, err := s.loadCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		_ = s.recordRetrievalRun(ctx, req, nil, nil, nil, false, "", "empty", start)
		return nil, nil
	}

	vectorUsed := false
	if s.embeddingProvider != nil && s.embeddingProvider.Enabled() {
		if hasVector, _ := s.hasPgVectorTable(ctx); hasVector {
			vectorUsed = true
			vectorCandidates, _ := s.vectorCandidates(ctx, req)
			candidates = mergeRetrievedTips(candidates, vectorCandidates)
		}
	}

	sortRetrievedTips(candidates)
	if len(candidates) > 12 {
		candidates = candidates[:12]
	}

	selected := candidates
	modelUsed := ""
	if s.claudeService != nil && len(candidates) > 1 {
		if reranked, model, rerankErr := s.rerankWithClaude(ctx, req, candidates, limit); rerankErr == nil && len(reranked) > 0 {
			selected = reranked
			modelUsed = model
		}
	}

	sortRetrievedTips(selected)
	if len(selected) > limit {
		selected = selected[:limit]
	}

	candidateIDs := tipIDs(candidates)
	rerankedIDs := tipIDs(selected)
	selectedIDs := tipIDs(selected)
	status := "completed"
	if len(selected) == 0 {
		status = "empty"
	}
	_ = s.recordRetrievalRun(ctx, req, candidateIDs, rerankedIDs, selectedIDs, vectorUsed, modelUsed, status, start)

	for _, item := range selected {
		_ = s.recordTipRetrieval(ctx, item.Tip.ID, req.TaskID, req.SessionID, item.Score, item.Reason)
	}

	return selected, nil
}

func (s *MemoryRetrievalService) loadCandidates(ctx context.Context, req MemoryRetrieveRequest) ([]RetrievedTip, error) {
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
		LIMIT 200
	`, req.OrgID, req.RepoFullName)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory tips: %w", err)
	}
	defer rows.Close()

	requestText := strings.Join([]string{req.TaskTitle, req.TaskDescription, req.TaskPrompt, req.Subtask, req.FailureSignature}, " ")
	requestTokens := uniqueTokens(tokenize(requestText))
	var ranked []RetrievedTip
	for rows.Next() {
		tip, err := scanMemoryTip(rows)
		if err != nil {
			return nil, err
		}
		score, reason := scoreTip(req, requestTokens, tip)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, RetrievedTip{Tip: tip, Score: score, Reason: reason})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating memory tips: %w", err)
	}
	sortRetrievedTips(ranked)
	if len(ranked) > 20 {
		ranked = ranked[:20]
	}
	return ranked, nil
}

func (s *MemoryRetrievalService) vectorCandidates(ctx context.Context, req MemoryRetrieveRequest) ([]RetrievedTip, error) {
	queryEmbedding, err := s.embeddingProvider.EmbedQuery(ctx, RetrievalEmbeddingQuery{
		Text: strings.Join([]string{req.TaskTitle, req.TaskDescription, req.TaskPrompt, req.Subtask, req.FailureSignature}, "\n"),
	})
	if err != nil || len(queryEmbedding.Values) == 0 {
		return nil, err
	}

	hasVector, err := s.hasPgVectorTable(ctx)
	if err != nil || !hasVector {
		return nil, err
	}

	vectorJSON, _ := json.Marshal(queryEmbedding.Values)
	rows, err := s.db.Pool.Query(ctx, `
		SELECT t.id, t.org_id, t.repo_full_name, t.source_branch, t.tip_type, t.scope, t.title, t.content,
		       COALESCE(t.trigger_condition, ''), t.action_steps, t.priority, t.confidence, t.canonical_key,
		       COALESCE(t.failure_signature, ''), COALESCE(t.task_fingerprint, ''), t.keywords,
		       COALESCE(t.search_text, ''), COALESCE(t.semantic_summary, ''), COALESCE(t.outcome_class, ''),
		       COALESCE(t.embedding_status, 'disabled'), COALESCE(t.embedding_model, ''), t.embedding_updated_at,
		       t.evidence_count, t.use_count, t.last_retrieved_at, t.created_at, t.updated_at
		FROM memory_tips t
		INNER JOIN memory_tip_embeddings e ON e.tip_id = t.id
		WHERE t.org_id = $1 AND t.repo_full_name = $2
		ORDER BY e.embedding_vector <=> $3::vector
		LIMIT 10
	`, req.OrgID, req.RepoFullName, string(vectorJSON))
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	var items []RetrievedTip
	for rows.Next() {
		tip, err := scanMemoryTip(rows)
		if err != nil {
			return nil, err
		}
		score := 2.0
		if req.FailureSignature != "" && tip.FailureSignature == req.FailureSignature {
			score += 2.0
		}
		items = append(items, RetrievedTip{
			Tip:    tip,
			Score:  score,
			Reason: "vector similarity",
		})
	}
	return items, nil
}

func (s *MemoryRetrievalService) rerankWithClaude(ctx context.Context, req MemoryRetrieveRequest, candidates []RetrievedTip, limit int) ([]RetrievedTip, string, error) {
	type candidate struct {
		ID               string   `json:"id"`
		Title            string   `json:"title"`
		Type             string   `json:"type"`
		Priority         string   `json:"priority"`
		Content          string   `json:"content"`
		TriggerCondition string   `json:"trigger_condition"`
		FailureSignature string   `json:"failure_signature"`
		ActionSteps      []string `json:"action_steps"`
		Reason           string   `json:"reason"`
		BaseScore        float64  `json:"base_score"`
	}
	payload := struct {
		TaskTitle        string      `json:"task_title"`
		TaskDescription  string      `json:"task_description"`
		TaskPrompt       string      `json:"task_prompt"`
		Subtask          string      `json:"subtask"`
		FailureSignature string      `json:"failure_signature"`
		Limit            int         `json:"limit"`
		Candidates       []candidate `json:"candidates"`
	}{
		TaskTitle:        req.TaskTitle,
		TaskDescription:  req.TaskDescription,
		TaskPrompt:       req.TaskPrompt,
		Subtask:          req.Subtask,
		FailureSignature: req.FailureSignature,
		Limit:            limit,
		Candidates:       make([]candidate, 0, len(candidates)),
	}
	for _, item := range candidates {
		payload.Candidates = append(payload.Candidates, candidate{
			ID:               item.Tip.ID,
			Title:            item.Tip.Title,
			Type:             item.Tip.TipType,
			Priority:         item.Tip.Priority,
			Content:          item.Tip.Content,
			TriggerCondition: item.Tip.TriggerCondition,
			FailureSignature: item.Tip.FailureSignature,
			ActionSteps:      item.Tip.ActionSteps,
			Reason:           item.Reason,
			BaseScore:        item.Score,
		})
	}
	body, _ := json.MarshalIndent(payload, "", "  ")
	prompt := fmt.Sprintf(`Select the most relevant durable guidance for a coding task.

Return ONLY JSON with this schema:
{
  "selected": [
    {"id": "tip id", "reason": "one line explanation", "score": 0.0}
  ]
}

Rules:
- Prefer recovery tips when failure_signature is present.
- Prefer tips that directly match the task, subtask, or failure mode.
- Select at most %d tips.
- Do not select redundant tips that say the same thing.

Input:
%s`, limit, string(body))

	text, model, err := s.claudeService.Complete(ctx, req.OrgID, prompt, 1800)
	if err != nil {
		return nil, "", err
	}
	var response struct {
		Selected []struct {
			ID     string  `json:"id"`
			Reason string  `json:"reason"`
			Score  float64 `json:"score"`
		} `json:"selected"`
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "```json"), "```"))
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		return nil, "", err
	}
	if len(response.Selected) == 0 {
		return nil, model, nil
	}

	candidateByID := make(map[string]RetrievedTip, len(candidates))
	for _, item := range candidates {
		candidateByID[item.Tip.ID] = item
	}
	selected := make([]RetrievedTip, 0, len(response.Selected))
	for _, item := range response.Selected {
		candidate, ok := candidateByID[item.ID]
		if !ok {
			continue
		}
		if item.Score > 0 {
			candidate.Score += item.Score
		}
		if item.Reason != "" {
			candidate.Reason = item.Reason
		}
		selected = append(selected, candidate)
	}
	return selected, model, nil
}

func (s *MemoryRetrievalService) hasPgVectorTable(ctx context.Context) (bool, error) {
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

func (s *MemoryRetrievalService) recordRetrievalRun(ctx context.Context, req MemoryRetrieveRequest, candidateIDs, rerankedIDs, selectedIDs []string, vectorUsed bool, model, status string, started time.Time) error {
	candidateJSON, _ := json.Marshal(candidateIDs)
	rerankedJSON, _ := json.Marshal(rerankedIDs)
	selectedJSON, _ := json.Marshal(selectedIDs)
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO retrieval_runs (
			id, org_id, repo_full_name, task_id, session_id, query_text, subtask, failure_signature,
			candidate_tip_ids, reranked_tip_ids, selected_tip_ids, vector_search_used,
			reranker_model, status, latency_ms, created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW()
		)
	`, uuid.New().String(), req.OrgID, req.RepoFullName, nullableString(req.TaskID), nullableString(req.SessionID),
		strings.Join([]string{req.TaskTitle, req.TaskDescription, req.TaskPrompt}, "\n"), req.Subtask,
		req.FailureSignature, candidateJSON, rerankedJSON, selectedJSON, vectorUsed,
		model, firstNonEmpty(status, "completed"), int(time.Since(started).Milliseconds()))
	return err
}

func (s *MemoryRetrievalService) recordTipRetrieval(ctx context.Context, tipID, taskID, sessionID string, score float64, reason string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO memory_tip_retrievals (id, tip_id, task_id, session_id, score, reason, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW())
	`, uuid.New().String(), tipID, nullableString(taskID), nullableString(sessionID), score, reason)
	if err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		UPDATE memory_tips
		SET use_count = use_count + 1, last_retrieved_at = NOW()
		WHERE id = $1
	`, tipID)
	return err
}

func sortRetrievedTips(items []RetrievedTip) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			if items[i].Tip.Priority == items[j].Tip.Priority {
				return items[i].Tip.UpdatedAt.After(items[j].Tip.UpdatedAt)
			}
			return priorityWeight(items[i].Tip.Priority) > priorityWeight(items[j].Tip.Priority)
		}
		return items[i].Score > items[j].Score
	})
}

func mergeRetrievedTips(base []RetrievedTip, extras []RetrievedTip) []RetrievedTip {
	seen := make(map[string]RetrievedTip, len(base)+len(extras))
	for _, item := range append(base, extras...) {
		existing, ok := seen[item.Tip.ID]
		if !ok || item.Score > existing.Score {
			seen[item.Tip.ID] = item
		}
	}
	merged := make([]RetrievedTip, 0, len(seen))
	for _, item := range seen {
		merged = append(merged, item)
	}
	return merged
}

func tipIDs(items []RetrievedTip) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.Tip != nil && item.Tip.ID != "" {
			ids = append(ids, item.Tip.ID)
		}
	}
	return ids
}
