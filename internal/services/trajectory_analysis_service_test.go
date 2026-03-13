package services

import (
	"testing"
	"time"

	"github.com/gradient/gradient/internal/models"
)

func TestHeuristicAnalysisDetectsRecovery(t *testing.T) {
	svc := &TrajectoryAnalysisService{}
	task := &models.AgentTask{
		ID:            "task-1",
		OrgID:         "org-1",
		RepoFullName:  "acme/repo",
		Branch:        "main",
		Status:        "complete",
		Title:         "Fix login flow",
		OutputSummary: "Recovered after refreshing credentials",
	}

	events := []trajectoryEvent{
		{
			ID:               "evt-1",
			EventType:        "subtask_marked",
			Subtask:          "login flow",
			Outcome:          "failed",
			Summary:          "Login failed with invalid credentials",
			FailureSignature: "invalid-credentials",
			CreatedAt:        time.Now(),
		},
		{
			ID:               "evt-2",
			EventType:        "subtask_marked",
			Subtask:          "login flow",
			Outcome:          "completed",
			Summary:          "Recovered after refreshing credentials",
			FailureSignature: "invalid-credentials",
			CreatedAt:        time.Now().Add(time.Second),
		},
	}

	subtasks := collectSubtasks(task, nil, nil, events)
	analysis := svc.heuristicAnalysis(&normalizedTrajectory{
		Task:     task,
		Events:   events,
		Subtasks: subtasks,
		Summary:  task.OutputSummary,
	})

	if analysis.OutcomeClass != "recovered" {
		t.Fatalf("expected recovered outcome, got %s", analysis.OutcomeClass)
	}
	if analysis.RecoveryAction == "" {
		t.Fatalf("expected recovery action to be populated")
	}
}

func TestHeuristicAnalysisDetectsInefficiency(t *testing.T) {
	svc := &TrajectoryAnalysisService{}
	task := &models.AgentTask{
		ID:            "task-2",
		OrgID:         "org-1",
		RepoFullName:  "acme/repo",
		Branch:        "main",
		Status:        "complete",
		Title:         "Reduce retry loop",
		OutputSummary: "Task completed after repeated passes",
	}

	bundles := []*models.ChangeBundle{
		{
			ID:        "bundle-1",
			SessionID: "session-1",
			ContextDiff: map[string]interface{}{
				"subtask": "validation",
				"summary": "Ran validation once",
				"outcome": "completed",
			},
			DecisionDiff: map[string]interface{}{
				"subtask": "validation",
				"summary": "Ran validation once",
				"outcome": "completed",
			},
		},
		{
			ID:        "bundle-2",
			SessionID: "session-1",
			ContextDiff: map[string]interface{}{
				"subtask": "validation",
				"summary": "Ran validation again",
				"outcome": "completed",
			},
			DecisionDiff: map[string]interface{}{
				"subtask": "validation",
				"summary": "Ran validation again",
				"outcome": "completed",
			},
		},
	}

	subtasks := collectSubtasks(task, nil, bundles, nil)
	analysis := svc.heuristicAnalysis(&normalizedTrajectory{
		Task:     task,
		Bundles:  bundles,
		Subtasks: subtasks,
		Summary:  task.OutputSummary,
	})

	if analysis.OutcomeClass != "inefficient_success" {
		t.Fatalf("expected inefficient_success, got %s", analysis.OutcomeClass)
	}
	if analysis.RecommendedAlternative == "" {
		t.Fatalf("expected recommended alternative to be populated")
	}
}
