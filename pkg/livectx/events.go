// Package livectx implements the Live Context Mesh — real-time, structured
// context sharing between running environments on the same branch.
//
// Architecture:
//
//	Agent A → publish → NATS JetStream → subscribe → Agent B
//	                        ↓
//	                   API Server → PostgreSQL (durable log)
//
// Events are modeled as a grow-only, append-only log (CRDT-friendly).
// Each event is immutable and identified by a globally unique ID.
// Conflict resolution is handled by consumers reading the full log
// and applying domain-specific merge logic.
package livectx

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is the current event schema version.
// Consumers MUST check this and handle unknown versions gracefully.
const SchemaVersion = 1

// EventType enumerates the kinds of structured context events.
type EventType string

const (
	EventPackageInstalled EventType = "package_installed"
	EventPackageRemoved   EventType = "package_removed"
	EventTestFailed       EventType = "test_failed"
	EventTestFixed        EventType = "test_fixed"
	EventPatternLearned   EventType = "pattern_learned"
	EventConfigChanged    EventType = "config_changed"
	EventDependencyAdded  EventType = "dependency_added"
	EventErrorEncountered EventType = "error_encountered"
	EventCommandRan       EventType = "command_ran"
	EventFileChanged      EventType = "file_changed"
	EventCustom           EventType = "custom"

	// Agent orchestration events
	EventBugDiscovered    EventType = "bug_discovered"
	EventContractUpdated  EventType = "contract_updated"
	EventPRCreated        EventType = "pr_created"
	EventPRUpdated        EventType = "pr_updated"
	EventConflictDetected EventType = "conflict_detected"
	EventContextStale     EventType = "context_stale"
	EventMergeSuccess     EventType = "merge_success"
	EventAgentReactivated EventType = "agent_reactivated"
)

// validEventTypes is the canonical set of valid event types.
var validEventTypes = map[EventType]bool{
	EventPackageInstalled: true,
	EventPackageRemoved:   true,
	EventTestFailed:       true,
	EventTestFixed:        true,
	EventPatternLearned:   true,
	EventConfigChanged:    true,
	EventDependencyAdded:  true,
	EventErrorEncountered: true,
	EventCommandRan:       true,
	EventFileChanged:      true,
	EventCustom:           true,

	// Agent orchestration events
	EventBugDiscovered:    true,
	EventContractUpdated:  true,
	EventPRCreated:        true,
	EventPRUpdated:        true,
	EventConflictDetected: true,
	EventContextStale:     true,
	EventMergeSuccess:     true,
	EventAgentReactivated: true,
}

// IsValidEventType returns true if the event type is recognized.
func IsValidEventType(t EventType) bool {
	return validEventTypes[t]
}

// Event is the core unit of the Live Context Mesh.
// Events are immutable, globally unique, and form an append-only log.
type Event struct {
	// Identity
	ID            string    `json:"id"`             // UUIDv4 — globally unique
	SchemaVersion int       `json:"schema_version"` // Event schema version
	Type          EventType `json:"type"`           // Event type (enum)

	// Scoping
	OrgID  string `json:"org_id"` // Organization ID
	Branch string `json:"branch"` // Git branch this event belongs to
	EnvID  string `json:"env_id"` // Environment that produced this event
	Source string `json:"source"` // Source identifier (e.g. "agent", "api", "cli")

	// Payload — type-specific structured data
	Data json.RawMessage `json:"data"` // Type-specific payload (see *Data structs)

	// Deduplication
	IdempotencyKey string `json:"idempotency_key,omitempty"` // Optional client-supplied dedup key

	// Causality — enables ordering without vector clocks
	Timestamp time.Time `json:"timestamp"`           // Wall-clock time of event creation
	Sequence  int64     `json:"sequence,omitempty"`  // Server-assigned monotonic sequence (filled by store)
	CausalID  string    `json:"causal_id,omitempty"` // Optional: ID of event this was caused by
	ParentID  string    `json:"parent_id,omitempty"` // Optional: parent event for threading

	// Metadata
	CreatedAt time.Time `json:"created_at"`           // Server-side insertion time
	ExpiresAt time.Time `json:"expires_at,omitempty"` // Optional TTL for auto-cleanup
	Acked     bool      `json:"acked,omitempty"`      // Whether this event was acknowledged by at least one peer
}

// --- Type-specific payloads ---

// PackageData is the payload for package_installed / package_removed events.
type PackageData struct {
	Manager string `json:"manager"`           // pip, npm, apt, cargo, go, etc.
	Name    string `json:"name"`              // Package name
	Version string `json:"version,omitempty"` // Installed version
	Command string `json:"command,omitempty"` // Full install command (for replay)
}

// TestData is the payload for test_failed / test_fixed events.
type TestData struct {
	Test      string `json:"test"`                // Test name/path
	Error     string `json:"error,omitempty"`     // Error message (for failures)
	Fix       string `json:"fix,omitempty"`       // Fix description (for fixes)
	Duration  string `json:"duration,omitempty"`  // How long the test took
	Framework string `json:"framework,omitempty"` // pytest, jest, go test, etc.
	ExitCode  int    `json:"exit_code,omitempty"` // Process exit code
}

// PatternData is the payload for pattern_learned events.
type PatternData struct {
	Key        string  `json:"key"`                  // Pattern identifier
	Value      string  `json:"value"`                // Pattern description/content
	Category   string  `json:"category,omitempty"`   // Category (e.g. "performance", "debugging")
	Confidence float64 `json:"confidence,omitempty"` // Confidence score 0.0-1.0
	Supersedes string  `json:"supersedes,omitempty"` // ID of pattern this replaces
}

// ConfigData is the payload for config_changed events.
type ConfigData struct {
	Key      string `json:"key"`                 // Config key (e.g. "CUDA_VISIBLE_DEVICES")
	Value    string `json:"value"`               // New value
	OldValue string `json:"old_value,omitempty"` // Previous value
	Scope    string `json:"scope,omitempty"`     // "env", "user", "system"
}

// DependencyData is the payload for dependency_added events.
type DependencyData struct {
	File    string `json:"file"`               // requirements.txt, package.json, go.mod, etc.
	Name    string `json:"name"`               // Dependency name
	Version string `json:"version,omitempty"`  // Version constraint
	DevOnly bool   `json:"dev_only,omitempty"` // Dev dependency?
}

// ErrorData is the payload for error_encountered events.
type ErrorData struct {
	Error    string `json:"error"`               // Error message
	Command  string `json:"command,omitempty"`   // Command that caused it
	ExitCode int    `json:"exit_code,omitempty"` // Exit code
	Resolved bool   `json:"resolved,omitempty"`  // Has it been resolved?
	Solution string `json:"solution,omitempty"`  // How it was resolved
}

// CommandData is the payload for command_ran events.
type CommandData struct {
	Command  string `json:"command"`            // The command
	ExitCode int    `json:"exit_code"`          // Exit code
	Duration string `json:"duration,omitempty"` // How long it took
	Output   string `json:"output,omitempty"`   // Truncated output (max 4KB)
}

// FileChangeData is the payload for file_changed events.
type FileChangeData struct {
	Path         string `json:"path"`   // File path relative to workspace
	Action       string `json:"action"` // "created", "modified", "deleted"
	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
}

// CustomData is the payload for custom events.
type CustomData struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// BugDiscoveredData is the payload for bug_discovered events.
type BugDiscoveredData struct {
	AffectedFiles []string `json:"affected_files"`
	Description   string   `json:"description"`
	FixApplied    string   `json:"fix_applied,omitempty"`
	Severity      string   `json:"severity,omitempty"` // "low", "medium", "high", "critical"
	SessionID     string   `json:"session_id"`
	PRURL         string   `json:"pr_url,omitempty"`
}

// ContractUpdatedData is the payload for contract_updated events.
type ContractUpdatedData struct {
	ContractID string `json:"contract_id"`
	Type       string `json:"type"`
	Scope      string `json:"scope"`
	Action     string `json:"action"` // "created", "updated", "violated"
	SessionID  string `json:"session_id"`
}

// PREventData is the payload for pr_created / pr_updated events.
type PREventData struct {
	PRURL     string   `json:"pr_url"`
	TaskID    string   `json:"task_id"`
	SessionID string   `json:"session_id"`
	Branch    string   `json:"branch"`
	Files     []string `json:"files,omitempty"`
	Action    string   `json:"action,omitempty"` // "created", "updated", "amended"
}

// ConflictDetectedData is the payload for conflict_detected events.
type ConflictDetectedData struct {
	Type        string   `json:"type"` // "textual", "behavioral", "contractual"
	SessionA    string   `json:"session_a"`
	SessionB    string   `json:"session_b"`
	Files       []string `json:"files,omitempty"`
	Description string   `json:"description"`
}

// MergeSuccessData is the payload for merge_success events.
type MergeSuccessData struct {
	TaskID         string   `json:"task_id"`
	IntegrationRef string   `json:"integration_branch"`
	Sessions       []string `json:"sessions"`
	CommitSHA      string   `json:"commit_sha"`
}

// --- Constructors ---

// NewEvent creates a new Event with a fresh UUID, timestamp, and schema version.
func NewEvent(eventType EventType, orgID, branch, envID string, data interface{}) (*Event, error) {
	if !IsValidEventType(eventType) {
		return nil, fmt.Errorf("invalid event type: %q", eventType)
	}
	if orgID == "" {
		return nil, fmt.Errorf("org_id is required")
	}
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	if envID == "" {
		return nil, fmt.Errorf("env_id is required")
	}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event data: %w", err)
	}

	now := time.Now().UTC()
	return &Event{
		ID:            uuid.New().String(),
		SchemaVersion: SchemaVersion,
		Type:          eventType,
		OrgID:         orgID,
		Branch:        branch,
		EnvID:         envID,
		Source:        "agent",
		Data:          dataJSON,
		Timestamp:     now,
		CreatedAt:     now,
	}, nil
}

// WithSource sets the source field. Chainable.
func (e *Event) WithSource(source string) *Event {
	e.Source = source
	return e
}

// WithIdempotencyKey sets the idempotency key. Chainable.
func (e *Event) WithIdempotencyKey(key string) *Event {
	e.IdempotencyKey = key
	return e
}

// WithCausalID sets a causal link to another event. Chainable.
func (e *Event) WithCausalID(id string) *Event {
	e.CausalID = id
	return e
}

// WithTTL sets an expiration time for automatic cleanup. Chainable.
func (e *Event) WithTTL(ttl time.Duration) *Event {
	e.ExpiresAt = e.Timestamp.Add(ttl)
	return e
}

// --- Validation ---

// Validate checks that the event has all required fields and valid data.
func (e *Event) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event ID is required")
	}
	if e.SchemaVersion < 1 {
		return fmt.Errorf("schema_version must be >= 1")
	}
	if !IsValidEventType(e.Type) {
		return fmt.Errorf("invalid event type: %q", e.Type)
	}
	if e.OrgID == "" {
		return fmt.Errorf("org_id is required")
	}
	if e.Branch == "" {
		return fmt.Errorf("branch is required")
	}
	if e.EnvID == "" {
		return fmt.Errorf("env_id is required")
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("data payload is required")
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}

	// Validate data payload is valid JSON
	var raw json.RawMessage
	if err := json.Unmarshal(e.Data, &raw); err != nil {
		return fmt.Errorf("data is not valid JSON: %w", err)
	}

	// Type-specific validation
	switch e.Type {
	case EventPackageInstalled, EventPackageRemoved:
		var d PackageData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return fmt.Errorf("invalid package data: %w", err)
		}
		if d.Manager == "" || d.Name == "" {
			return fmt.Errorf("package events require manager and name")
		}
	case EventTestFailed, EventTestFixed:
		var d TestData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return fmt.Errorf("invalid test data: %w", err)
		}
		if d.Test == "" {
			return fmt.Errorf("test events require test name")
		}
	case EventPatternLearned:
		var d PatternData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return fmt.Errorf("invalid pattern data: %w", err)
		}
		if d.Key == "" || d.Value == "" {
			return fmt.Errorf("pattern events require key and value")
		}
	case EventConfigChanged:
		var d ConfigData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return fmt.Errorf("invalid config data: %w", err)
		}
		if d.Key == "" {
			return fmt.Errorf("config events require key")
		}
	}

	return nil
}

// --- Serialization ---

// Marshal serializes the event to JSON.
func (e *Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEvent deserializes an event from JSON.
func UnmarshalEvent(data []byte) (*Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}
	return &e, nil
}

// --- NATS Subject ---

// NATSSubject returns the NATS subject for this event's scope.
// Format: ctx.<org_id>.<branch_safe>
func (e *Event) NATSSubject() string {
	return NATSSubject(e.OrgID, e.Branch)
}

// NATSSubject constructs the NATS subject for a given org and branch.
// Branch names are sanitized: slashes become dots, other special chars removed.
func NATSSubject(orgID, branch string) string {
	safe := sanitizeBranchForNATS(branch)
	return fmt.Sprintf("ctx.%s.%s", orgID, safe)
}

// NATSSubjectWildcard returns a wildcard subject for all branches in an org.
func NATSSubjectWildcard(orgID string) string {
	return fmt.Sprintf("ctx.%s.>", orgID)
}

// sanitizeBranchForNATS converts a git branch name to a valid NATS subject token.
// e.g. "feature/new-algo" → "feature.new-algo"
func sanitizeBranchForNATS(branch string) string {
	// Replace slashes with dots (NATS subject hierarchy)
	s := strings.ReplaceAll(branch, "/", ".")
	// Remove any remaining invalid NATS characters
	var result strings.Builder
	for _, c := range s {
		if c == '.' || c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result.WriteRune(c)
		}
	}
	return result.String()
}

// --- Idempotency ---

// ComputeIdempotencyKey generates a deterministic idempotency key from event content.
// Used for deduplication when the client doesn't supply one.
func ComputeIdempotencyKey(eventType EventType, envID string, data []byte) string {
	h := sha256.New()
	h.Write([]byte(string(eventType)))
	h.Write([]byte(envID))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

// --- Query helpers ---

// EventFilter is used to query events from the store.
type EventFilter struct {
	OrgID  string      `json:"org_id"`
	Branch string      `json:"branch,omitempty"`
	EnvID  string      `json:"env_id,omitempty"`
	Types  []EventType `json:"types,omitempty"`
	Since  time.Time   `json:"since,omitempty"`
	Until  time.Time   `json:"until,omitempty"`
	MinSeq int64       `json:"min_seq,omitempty"` // For cursor-based pagination
	Limit  int         `json:"limit,omitempty"`
	Source string      `json:"source,omitempty"`
}

// EventBatch is a group of events returned from a query, with cursor info.
type EventBatch struct {
	Events  []*Event `json:"events"`
	HasMore bool     `json:"has_more"`
	LastSeq int64    `json:"last_seq"` // Use as min_seq for next page
	Count   int      `json:"count"`
}

// EventStats aggregates event statistics for a branch.
type EventStats struct {
	OrgID         string           `json:"org_id"`
	Branch        string           `json:"branch"`
	TotalEvents   int64            `json:"total_events"`
	ActiveEnvs    int              `json:"active_envs"`
	EventsByType  map[string]int64 `json:"events_by_type"`
	LastEventAt   time.Time        `json:"last_event_at,omitempty"`
	OldestEventAt time.Time        `json:"oldest_event_at,omitempty"`
}
