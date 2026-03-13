package services

import (
	"testing"
	"time"

	"github.com/gradient/gradient/internal/models"
)

func TestCollectSubtasksDetectsRecovery(t *testing.T) {
	task := &models.AgentTask{
		ID:            "task-1",
		Title:         "Fix login flow",
		Description:   "Repair the login flow after a credential error",
		Status:        "complete",
		OutputSummary: "Recovered by updating credential lookup",
	}

	events := []trajectoryEvent{
		{
			ID:               "evt-1",
			EventType:        "subtask_marked",
			SessionID:        "session-1",
			Subtask:          "login flow",
			Outcome:          "failed",
			Summary:          "Initial login attempt failed",
			FailureSignature: "invalid-credentials",
			CreatedAt:        time.Now(),
		},
		{
			ID:               "evt-2",
			EventType:        "subtask_marked",
			SessionID:        "session-1",
			Subtask:          "login flow",
			Outcome:          "completed",
			Summary:          "Recovered after refreshing credentials",
			FailureSignature: "invalid-credentials",
			CreatedAt:        time.Now().Add(time.Second),
		},
	}

	subtasks := collectSubtasks(task, nil, nil, events)
	if !trajectoryHasRecovery(subtasks) {
		t.Fatalf("expected recovery trajectory to be detected")
	}

	loginFlow := subtasks[normalizedKey("login flow")]
	if loginFlow == nil {
		t.Fatalf("expected login flow subtask to be present")
	}
	if !loginFlow.Failed || !loginFlow.Succeeded {
		t.Fatalf("expected login flow to record both failure and success, got failed=%v succeeded=%v", loginFlow.Failed, loginFlow.Succeeded)
	}
}

func TestScoreTipPrioritizesFailureSignature(t *testing.T) {
	req := MemoryRetrieveRequest{
		OrgID:            "org-1",
		RepoFullName:     "acme/repo",
		Branch:           "main",
		TaskTitle:        "Fix login bug",
		TaskDescription:  "Repair credential handling",
		TaskPrompt:       "Recover from invalid credentials during login",
		FailureSignature: "invalid-credentials",
	}
	requestTokens := uniqueTokens(tokenize(req.TaskTitle + " " + req.TaskDescription + " " + req.TaskPrompt))

	recoveryTip := &models.MemoryTip{
		RepoFullName:     "acme/repo",
		SourceBranch:     "main",
		TipType:          "recovery",
		Priority:         "high",
		FailureSignature: "invalid-credentials",
		Keywords:         []string{"login", "credentials"},
		SearchText:       "login credentials recovery",
	}
	strategyTip := &models.MemoryTip{
		RepoFullName: "acme/repo",
		SourceBranch: "feature/demo",
		TipType:      "strategy",
		Priority:     "medium",
		Keywords:     []string{"login", "validation"},
		SearchText:   "login validation strategy",
	}

	recoveryScore, _ := scoreTip(req, requestTokens, recoveryTip)
	strategyScore, _ := scoreTip(req, requestTokens, strategyTip)
	if recoveryScore <= strategyScore {
		t.Fatalf("expected recovery tip with exact failure signature to outrank strategy tip, got recovery=%v strategy=%v", recoveryScore, strategyScore)
	}
}
