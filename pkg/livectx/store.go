package livectx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gradient/gradient/internal/db"
)

// EventStore persists context events to PostgreSQL.
// It provides durable storage, cursor-based pagination, deduplication,
// and TTL-based cleanup for the Live Context Mesh.
type EventStore struct {
	db *db.DB
}

// NewEventStore creates a new event store backed by PostgreSQL.
func NewEventStore(database *db.DB) *EventStore {
	return &EventStore{db: database}
}

// Publish persists an event to the database.
// It handles idempotency: if an event with the same idempotency_key already
// exists for the same org+branch, the insert is skipped (no error).
// Returns the server-assigned sequence number.
func (s *EventStore) Publish(ctx context.Context, event *Event) (int64, error) {
	if err := event.Validate(); err != nil {
		return 0, fmt.Errorf("event validation failed: %w", err)
	}

	// Auto-compute idempotency key if not supplied
	if event.IdempotencyKey == "" {
		event.IdempotencyKey = ComputeIdempotencyKey(event.Type, event.EnvID, event.Data)
	}

	now := time.Now().UTC()
	event.CreatedAt = now

	query := `
		INSERT INTO context_events (
			id, schema_version, event_type, org_id, repo_full_name, branch, env_id, source,
			data, idempotency_key, timestamp, causal_id, parent_id,
			created_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13,
			$14, $15
		)
		ON CONFLICT (org_id, branch, idempotency_key) DO NOTHING
		RETURNING sequence
	`

	var expiresAt *time.Time
	if !event.ExpiresAt.IsZero() {
		expiresAt = &event.ExpiresAt
	}

	var seq int64
	err := s.db.Pool.QueryRow(ctx, query,
		event.ID,
		event.SchemaVersion,
		string(event.Type),
		event.OrgID,
		event.RepoFullName,
		event.Branch,
		event.EnvID,
		event.Source,
		event.Data,
		event.IdempotencyKey,
		event.Timestamp,
		nilIfEmpty(event.CausalID),
		nilIfEmpty(event.ParentID),
		event.CreatedAt,
		expiresAt,
	).Scan(&seq)

	if err != nil {
		// Check if it was a duplicate (ON CONFLICT DO NOTHING returns no rows)
		if err.Error() == "no rows in result set" {
			// Duplicate — return 0 with no error
			return 0, nil
		}
		return 0, fmt.Errorf("failed to insert event: %w", err)
	}

	event.Sequence = seq
	return seq, nil
}

// Query retrieves events matching the given filter.
// Results are ordered by sequence (ascending) for deterministic replay.
func (s *EventStore) Query(ctx context.Context, filter EventFilter) (*EventBatch, error) {
	if filter.OrgID == "" {
		return nil, fmt.Errorf("org_id is required for event queries")
	}

	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	// Build query dynamically based on filter
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("org_id = $%d", argIdx))
	args = append(args, filter.OrgID)
	argIdx++

	if filter.RepoFullName != "" {
		conditions = append(conditions, fmt.Sprintf("repo_full_name = $%d", argIdx))
		args = append(args, filter.RepoFullName)
		argIdx++
	}

	if filter.Branch != "" {
		conditions = append(conditions, fmt.Sprintf("branch = $%d", argIdx))
		args = append(args, filter.Branch)
		argIdx++
	}

	if filter.EnvID != "" {
		conditions = append(conditions, fmt.Sprintf("env_id = $%d", argIdx))
		args = append(args, filter.EnvID)
		argIdx++
	}

	if len(filter.Types) > 0 {
		placeholders := make([]string, len(filter.Types))
		for i, t := range filter.Types {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, string(t))
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("event_type IN (%s)", strings.Join(placeholders, ",")))
	}

	if !filter.Since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, filter.Since)
		argIdx++
	}

	if !filter.Until.IsZero() {
		conditions = append(conditions, fmt.Sprintf("timestamp <= $%d", argIdx))
		args = append(args, filter.Until)
		argIdx++
	}

	if filter.MinSeq > 0 {
		conditions = append(conditions, fmt.Sprintf("sequence > $%d", argIdx))
		args = append(args, filter.MinSeq)
		argIdx++
	}

	if filter.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argIdx))
		args = append(args, filter.Source)
		argIdx++
	}

	// Exclude expired events
	conditions = append(conditions, fmt.Sprintf("(expires_at IS NULL OR expires_at > $%d)", argIdx))
	args = append(args, time.Now().UTC())
	argIdx++

	whereClause := strings.Join(conditions, " AND ")

	// Fetch limit+1 to detect hasMore
	query := fmt.Sprintf(`
		SELECT id, schema_version, event_type, org_id, COALESCE(repo_full_name, ''), branch, env_id, source,
		       data, idempotency_key, timestamp, sequence, causal_id, parent_id,
		       created_at, expires_at, acked
		FROM context_events
		WHERE %s
		ORDER BY sequence ASC
		LIMIT $%d
	`, whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}

	var lastSeq int64
	if len(events) > 0 {
		lastSeq = events[len(events)-1].Sequence
	}

	return &EventBatch{
		Events:  events,
		HasMore: hasMore,
		LastSeq: lastSeq,
		Count:   len(events),
	}, nil
}

// GetByID retrieves a single event by its ID.
func (s *EventStore) GetByID(ctx context.Context, id string) (*Event, error) {
	query := `
		SELECT id, schema_version, event_type, org_id, COALESCE(repo_full_name, ''), branch, env_id, source,
		       data, idempotency_key, timestamp, sequence, causal_id, parent_id,
		       created_at, expires_at, acked
		FROM context_events
		WHERE id = $1
	`

	row := s.db.Pool.QueryRow(ctx, query, id)
	return scanEventRow(row)
}

// GetStats returns aggregate statistics for a branch's event stream.
func (s *EventStore) GetStats(ctx context.Context, orgID, branch string) (*EventStats, error) {
	query := `
		SELECT
			COUNT(*) as total,
			COUNT(DISTINCT env_id) as active_envs,
			MAX(timestamp) as last_event,
			MIN(timestamp) as oldest_event
		FROM context_events
		WHERE org_id = $1 AND branch = $2
			AND (expires_at IS NULL OR expires_at > NOW())
	`

	stats := &EventStats{
		OrgID:        orgID,
		Branch:       branch,
		EventsByType: make(map[string]int64),
	}

	var lastEvent, oldestEvent *time.Time
	err := s.db.Pool.QueryRow(ctx, query, orgID, branch).Scan(
		&stats.TotalEvents,
		&stats.ActiveEnvs,
		&lastEvent,
		&oldestEvent,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get event stats: %w", err)
	}

	if lastEvent != nil {
		stats.LastEventAt = *lastEvent
	}
	if oldestEvent != nil {
		stats.OldestEventAt = *oldestEvent
	}

	// Get breakdown by type
	typeQuery := `
		SELECT event_type, COUNT(*)
		FROM context_events
		WHERE org_id = $1 AND branch = $2
			AND (expires_at IS NULL OR expires_at > NOW())
		GROUP BY event_type
	`

	rows, err := s.db.Pool.Query(ctx, typeQuery, orgID, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get event type stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventType string
		var count int64
		if err := rows.Scan(&eventType, &count); err != nil {
			return nil, fmt.Errorf("failed to scan type stats: %w", err)
		}
		stats.EventsByType[eventType] = count
	}

	return stats, nil
}

// AckEvent marks an event as acknowledged by at least one peer.
func (s *EventStore) AckEvent(ctx context.Context, eventID string) error {
	query := `UPDATE context_events SET acked = true WHERE id = $1`
	_, err := s.db.Pool.Exec(ctx, query, eventID)
	return err
}

// CleanupExpired removes events past their TTL. Should be called periodically.
func (s *EventStore) CleanupExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM context_events WHERE expires_at IS NOT NULL AND expires_at <= NOW()`
	tag, err := s.db.Pool.Exec(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CleanupOlderThan removes events older than the given duration.
// This is a safety valve to prevent unbounded growth.
func (s *EventStore) CleanupOlderThan(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	query := `DELETE FROM context_events WHERE created_at < $1`
	tag, err := s.db.Pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// --- Row scanning helpers ---

// scannable is an interface for pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dest ...interface{}) error
}

func scanEvent(rows scannable) (*Event, error) {
	var e Event
	var eventType string
	var data []byte
	var causalID, parentID, idempotencyKey *string
	var expiresAt *time.Time

	err := rows.Scan(
		&e.ID,
		&e.SchemaVersion,
		&eventType,
		&e.OrgID,
		&e.RepoFullName,
		&e.Branch,
		&e.EnvID,
		&e.Source,
		&data,
		&idempotencyKey,
		&e.Timestamp,
		&e.Sequence,
		&causalID,
		&parentID,
		&e.CreatedAt,
		&expiresAt,
		&e.Acked,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan event: %w", err)
	}

	e.Type = EventType(eventType)
	e.Data = json.RawMessage(data)
	if causalID != nil {
		e.CausalID = *causalID
	}
	if parentID != nil {
		e.ParentID = *parentID
	}
	if idempotencyKey != nil {
		e.IdempotencyKey = *idempotencyKey
	}
	if expiresAt != nil {
		e.ExpiresAt = *expiresAt
	}

	return &e, nil
}

func scanEventRow(row scannable) (*Event, error) {
	return scanEvent(row)
}

// DeleteByRepoBranch removes all events for a specific repo+branch.
func (s *EventStore) DeleteByRepoBranch(ctx context.Context, orgID, repoFullName, branch string) (int64, error) {
	query := `DELETE FROM context_events WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`
	tag, err := s.db.Pool.Exec(ctx, query, orgID, repoFullName, branch)
	if err != nil {
		return 0, fmt.Errorf("failed to delete events for branch: %w", err)
	}
	return tag.RowsAffected(), nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
