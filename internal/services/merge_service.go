package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/pkg/env"
	"github.com/gradient/gradient/pkg/livectx"
)

// ConflictType classifies merge conflicts.
type ConflictType string

const (
	ConflictTextual      ConflictType = "textual"
	ConflictBehavioral   ConflictType = "behavioral"
	ConflictContractual  ConflictType = "contractual"
)

// MergeConflict represents a detected conflict between agent branches.
type MergeConflict struct {
	ID          string       `json:"id"`
	TaskID      string       `json:"task_id"`
	Type        ConflictType `json:"type"`
	SessionA    string       `json:"session_a"`
	SessionB    string       `json:"session_b"`
	Description string       `json:"description"`
	Files       []string     `json:"files,omitempty"`
	Resolved    bool         `json:"resolved"`
	Resolution  string       `json:"resolution,omitempty"`
	DetectedAt  time.Time    `json:"detected_at"`
}

// MergeStatus tracks the health of an integration branch.
type MergeStatus struct {
	TaskID         string          `json:"task_id"`
	IntegrationRef string          `json:"integration_branch"`
	Health         string          `json:"health"` // "green", "yellow", "red"
	Conflicts      []MergeConflict `json:"conflicts"`
	LastMergeAt    time.Time       `json:"last_merge_at"`
	SessionCount   int             `json:"session_count"`
}

// MergeService manages continuous merge simulation for multi-agent tasks.
type MergeService struct {
	db             *db.DB
	sessionService *SessionService
	envService     *EnvService
	meshPublisher  *livectx.MeshPublisher

	mu          sync.Mutex
	activeMerges map[string]context.CancelFunc // taskID -> cancel
	statuses     map[string]*MergeStatus       // taskID -> status
}

func NewMergeService(
	database *db.DB,
	sessionService *SessionService,
	envService *EnvService,
	meshPublisher *livectx.MeshPublisher,
) *MergeService {
	return &MergeService{
		db:             database,
		sessionService: sessionService,
		envService:     envService,
		meshPublisher:  meshPublisher,
		activeMerges:   make(map[string]context.CancelFunc),
		statuses:       make(map[string]*MergeStatus),
	}
}

// StartMergeSimulation begins continuous merge monitoring for a multi-agent task.
func (m *MergeService) StartMergeSimulation(taskID, orgID, baseBranch string) {
	m.mu.Lock()
	if _, exists := m.activeMerges[taskID]; exists {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.activeMerges[taskID] = cancel
	m.statuses[taskID] = &MergeStatus{
		TaskID:         taskID,
		IntegrationRef: fmt.Sprintf("integration/%s", taskID[:8]),
		Health:         "green",
		LastMergeAt:    time.Now(),
	}
	m.mu.Unlock()

	go m.mergeLoop(ctx, taskID, orgID, baseBranch)
}

// StopMergeSimulation stops the merge simulation for a task.
func (m *MergeService) StopMergeSimulation(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.activeMerges[taskID]; ok {
		cancel()
		delete(m.activeMerges, taskID)
	}
}

// GetMergeStatus returns the current merge status for a task.
func (m *MergeService) GetMergeStatus(taskID string) *MergeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statuses[taskID]
}

func (m *MergeService) mergeLoop(ctx context.Context, taskID, orgID, baseBranch string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[merge] Stopping merge simulation for task %s", taskID)
			return
		case <-ticker.C:
			m.runMergeCheck(ctx, taskID, orgID, baseBranch)
		}
	}
}

func (m *MergeService) runMergeCheck(ctx context.Context, taskID, orgID, baseBranch string) {
	sessions, err := m.sessionService.ListSessionsByTask(ctx, taskID)
	if err != nil {
		log.Printf("[merge] Failed to list sessions for task %s: %v", taskID, err)
		return
	}

	activeSessions := make([]*models.AgentSession, 0)
	for _, s := range sessions {
		if s.Status == "active" || s.Status == "completed" {
			activeSessions = append(activeSessions, s)
		}
	}

	if len(activeSessions) < 2 {
		return
	}

	conflicts := m.detectScopeConflicts(activeSessions)

	m.mu.Lock()
	status := m.statuses[taskID]
	if status != nil {
		status.Conflicts = conflicts
		status.LastMergeAt = time.Now()
		status.SessionCount = len(activeSessions)
		if len(conflicts) == 0 {
			status.Health = "green"
		} else {
			hasUnresolved := false
			for _, c := range conflicts {
				if !c.Resolved {
					hasUnresolved = true
					break
				}
			}
			if hasUnresolved {
				status.Health = "red"
			} else {
				status.Health = "yellow"
			}
		}
	}
	m.mu.Unlock()

	for _, conflict := range conflicts {
		if conflict.Resolved {
			continue
		}
		if m.meshPublisher != nil {
			evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, "merge-agent", map[string]interface{}{
				"key": "conflict_detected",
				"value": map[string]interface{}{
					"task_id":     taskID,
					"type":        string(conflict.Type),
					"session_a":   conflict.SessionA,
					"session_b":   conflict.SessionB,
					"description": conflict.Description,
					"files":       conflict.Files,
				},
			})
			if evt != nil {
				m.meshPublisher.Publish(ctx, evt.WithSource("merge-agent"))
			}
		}
	}
}

func (m *MergeService) detectScopeConflicts(sessions []*models.AgentSession) []MergeConflict {
	var conflicts []MergeConflict

	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			overlap := findPathOverlap(sessions[i].Scope.OwnedPaths, sessions[j].Scope.OwnedPaths)
			if len(overlap) > 0 {
				conflicts = append(conflicts, MergeConflict{
					ID:          fmt.Sprintf("%s-%s", sessions[i].ID[:8], sessions[j].ID[:8]),
					TaskID:      sessions[i].TaskID,
					Type:        ConflictTextual,
					SessionA:    sessions[i].ID,
					SessionB:    sessions[j].ID,
					Description: fmt.Sprintf("Overlapping owned paths between %s and %s", sessions[i].AgentRole, sessions[j].AgentRole),
					Files:       overlap,
					DetectedAt:  time.Now(),
				})
			}
		}
	}

	return conflicts
}

// findPathOverlap finds file paths that overlap between two path sets.
// A path overlaps if one is a prefix of the other.
func findPathOverlap(a, b []string) []string {
	var overlap []string
	for _, pa := range a {
		for _, pb := range b {
			if pa == pb || strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa) {
				overlap = append(overlap, pa)
				break
			}
		}
	}
	return overlap
}

// ValidateScope checks whether a file path is allowed for a given session scope.
func ValidateScope(scope models.SessionScope, filePath string) (bool, string) {
	for _, p := range scope.ReadOnlyPaths {
		if filePath == p || strings.HasPrefix(filePath, p) {
			return false, fmt.Sprintf("path %s is read-only for this session", filePath)
		}
	}

	if len(scope.OwnedPaths) > 0 {
		owned := false
		for _, p := range scope.OwnedPaths {
			if filePath == p || strings.HasPrefix(filePath, p) {
				owned = true
				break
			}
		}
		if !owned {
			return false, fmt.Sprintf("path %s is not in this session's owned paths", filePath)
		}
	}

	return true, ""
}

// SpawnParallelSessions creates multiple agent sessions for a decomposed task,
// each with isolated branches and environments.
func (m *MergeService) SpawnParallelSessions(
	ctx context.Context,
	orgID, taskID, baseBranch string,
	subTasks []SubTaskDefinition,
	managerSessionID string,
) ([]*models.AgentSession, error) {
	var sessions []*models.AgentSession

	for _, sub := range subTasks {
		branchName := fmt.Sprintf("task/%s/%s", taskID[:8], sub.Role)

		session := &models.AgentSession{
			TaskID:          taskID,
			ParentSessionID: &managerSessionID,
			OrgID:           orgID,
			AgentRole:       sub.Role,
			Scope: models.SessionScope{
				OwnedPaths:    sub.Scope.OwnedPaths,
				ReadOnlyPaths: sub.Scope.ReadOnlyPaths,
				TestSuites:    sub.Scope.TestSuites,
			},
			BranchName: branchName,
			Status:     "pending",
		}

		created, err := m.sessionService.CreateSession(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("failed to create session for role %s: %w", sub.Role, err)
		}

		sessions = append(sessions, created)
	}

	if len(sessions) >= 2 {
		m.StartMergeSimulation(taskID, orgID, baseBranch)
	}

	return sessions, nil
}

var _ env.Provider = nil
var _ = json.Marshal
