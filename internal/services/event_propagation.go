package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/pkg/livectx"
)

// EventPropagationService handles cross-PR event propagation,
// bug discovery flows, and reactive agent spawning.
type EventPropagationService struct {
	db             *db.DB
	sessionService *SessionService
	taskExecutor   *TaskExecutorService
	meshPublisher  *livectx.MeshPublisher
	eventBus       livectx.Bus
}

func NewEventPropagationService(
	database *db.DB,
	sessionService *SessionService,
	taskExecutor *TaskExecutorService,
	meshPublisher *livectx.MeshPublisher,
	eventBus livectx.Bus,
) *EventPropagationService {
	return &EventPropagationService{
		db:             database,
		sessionService: sessionService,
		taskExecutor:   taskExecutor,
		meshPublisher:  meshPublisher,
		eventBus:       eventBus,
	}
}

// StartListening subscribes to the Live Context Mesh for agent orchestration events.
func (ep *EventPropagationService) StartListening(ctx context.Context, orgID string) error {
	handler := func(handlerCtx context.Context, event *livectx.Event) error {
		switch event.Type {
		case livectx.EventBugDiscovered:
			return ep.handleBugDiscovered(handlerCtx, event)
		case livectx.EventContractUpdated:
			return ep.handleContractUpdated(handlerCtx, event)
		case livectx.EventConflictDetected:
			return ep.handleConflictDetected(handlerCtx, event)
		case livectx.EventContextStale:
			return ep.handleContextStale(handlerCtx, event)
		}
		return nil
	}

	subject := livectx.NATSSubjectWildcard(orgID)
	_ = subject // used implicitly by Subscribe
	return ep.eventBus.Subscribe(ctx, orgID, ">", "event-propagation", handler)
}

func (ep *EventPropagationService) handleBugDiscovered(ctx context.Context, event *livectx.Event) error {
	var bug livectx.BugDiscoveredData
	if err := json.Unmarshal(event.Data, &bug); err != nil {
		return fmt.Errorf("invalid bug_discovered payload: %w", err)
	}

	log.Printf("[propagation] Bug discovered by session %s: %s (files: %v)",
		bug.SessionID, bug.Description, bug.AffectedFiles)

	// Find all active sessions that have overlapping scopes
	affected, err := ep.findAffectedSessions(ctx, event.OrgID, bug.SessionID, bug.AffectedFiles)
	if err != nil {
		return err
	}

	// Record the bug as a context object for future reference
	ep.sessionService.UpsertContextObject(ctx, &models.ContextObject{
		OrgID:         event.OrgID,
		Branch:        event.Branch,
		Type:          "known_bug",
		Key:           fmt.Sprintf("bug-%s-%d", bug.SessionID[:8], time.Now().Unix()),
		Value:         event.Data,
		SourceSession: bug.SessionID,
	})

	for _, session := range affected {
		log.Printf("[propagation] Notifying session %s (%s) about bug in %v",
			session.ID, session.AgentRole, bug.AffectedFiles)

		// Publish reactivation event
		if ep.meshPublisher != nil {
			evt, _ := livectx.NewEvent(livectx.EventAgentReactivated, event.OrgID, session.BranchName, event.EnvID, map[string]interface{}{
				"session_id":  session.ID,
				"reason":      "bug_discovered",
				"bug_session": bug.SessionID,
				"description": bug.Description,
			})
			if evt != nil {
				ep.meshPublisher.Publish(ctx, evt.WithSource("propagation").WithCausalID(event.ID))
			}
		}
	}

	return nil
}

func (ep *EventPropagationService) handleContractUpdated(ctx context.Context, event *livectx.Event) error {
	var data livectx.ContractUpdatedData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil
	}

	log.Printf("[propagation] Contract %s %s by session %s", data.ContractID, data.Action, data.SessionID)

	if data.Action == "violated" {
		// Find consumers of the violated contract and notify them
		contract, err := ep.sessionService.GetContract(ctx, data.ContractID)
		if err != nil {
			return err
		}
		for _, consumerID := range contract.Consumers {
			session, _ := ep.sessionService.GetSession(ctx, consumerID)
			if session != nil && session.Status == "active" {
				log.Printf("[propagation] Contract violation affects session %s (%s)", session.ID, session.AgentRole)
			}
		}
	}

	return nil
}

func (ep *EventPropagationService) handleConflictDetected(ctx context.Context, event *livectx.Event) error {
	var data livectx.ConflictDetectedData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil
	}
	log.Printf("[propagation] Conflict detected between sessions %s and %s: %s",
		data.SessionA, data.SessionB, data.Description)
	return nil
}

func (ep *EventPropagationService) handleContextStale(ctx context.Context, event *livectx.Event) error {
	log.Printf("[propagation] Stale context event on branch %s", event.Branch)
	return nil
}

func (ep *EventPropagationService) findAffectedSessions(ctx context.Context, orgID, sourceSessionID string, affectedFiles []string) ([]*models.AgentSession, error) {
	rows, err := ep.db.Pool.Query(ctx, `
		SELECT id, task_id, parent_session_id, org_id, agent_role, scope, initial_sha,
			branch_name, environment_id, status, contracts, created_at, completed_at
		FROM agent_sessions
		WHERE org_id = $1 AND status IN ('active', 'completed') AND id != $2`,
		orgID, sourceSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var affected []*models.AgentSession
	for rows.Next() {
		session := &models.AgentSession{}
		var scopeJSON, contractsJSON string
		err := rows.Scan(
			&session.ID, &session.TaskID, &session.ParentSessionID, &session.OrgID,
			&session.AgentRole, &scopeJSON, &session.InitialSHA,
			&session.BranchName, &session.EnvironmentID, &session.Status,
			&contractsJSON, &session.CreatedAt, &session.CompletedAt,
		)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(scopeJSON), &session.Scope)
		json.Unmarshal([]byte(contractsJSON), &session.Contracts)

		// Check if this session's scope overlaps with affected files
		if scopeOverlaps(session.Scope, affectedFiles) {
			affected = append(affected, session)
		}
	}
	return affected, nil
}

func scopeOverlaps(scope models.SessionScope, files []string) bool {
	allPaths := append(scope.OwnedPaths, scope.ReadOnlyPaths...)
	if len(allPaths) == 0 {
		return true // no scope restriction = affected by everything
	}
	for _, file := range files {
		for _, path := range allPaths {
			if file == path || strings.HasPrefix(file, path) || strings.HasPrefix(path, file) {
				return true
			}
		}
	}
	return false
}
