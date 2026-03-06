package livectx

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// StreamName is the JetStream stream for live context events.
	StreamName = "GRADIENT_CTX"

	// StreamSubjects is the subject filter for the stream.
	StreamSubjects = "ctx.>"

	// MaxEventSize is the maximum size of a single event in bytes (64KB).
	MaxEventSize = 64 * 1024

	// DefaultMaxAge is the default retention period for events in JetStream.
	DefaultMaxAge = 7 * 24 * time.Hour // 7 days

	// DefaultMaxMsgs is the maximum number of messages per subject.
	DefaultMaxMsgs = 10000

	// ConsumerDurable is the prefix for durable consumer names.
	ConsumerDurable = "gradient-agent"

	// PublishTimeout is the timeout for publishing a single event.
	PublishTimeout = 5 * time.Second

	// ReconnectWait is the time between NATS reconnection attempts.
	ReconnectWait = 2 * time.Second

	// MaxReconnects is the maximum number of reconnection attempts (-1 = infinite).
	MaxReconnects = -1
)

// BusConfig holds configuration for the NATS event bus.
type BusConfig struct {
	// NATS connection URL (e.g. "nats://localhost:4222")
	URL string

	// MaxAge is the retention period for events in JetStream.
	MaxAge time.Duration

	// MaxMsgsPerSubject limits messages per subject (per branch).
	MaxMsgsPerSubject int64

	// ClientName identifies this client to the NATS server.
	ClientName string

	// AuthToken is an optional NATS auth token.
	AuthToken string
}

// EventHandler is called when an event is received from the mesh.
type EventHandler func(ctx context.Context, event *Event) error

// EventBus provides pub/sub for live context events over NATS JetStream.
// It handles:
//   - Publishing events with at-least-once delivery
//   - Subscribing to branch-scoped events with durable consumers
//   - Automatic reconnection and stream/consumer provisioning
//   - Rate limiting to prevent event floods
//   - Graceful shutdown
type EventBus struct {
	config BusConfig
	conn   *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream

	mu          sync.RWMutex
	consumers   map[string]jetstream.ConsumeContext // subject → consumer
	handlers    []EventHandler
	closed      bool

	// Rate limiting
	publishLimiter *rateLimiter
}

// NewEventBus creates a new event bus connected to NATS.
// It provisions the JetStream stream if it doesn't exist.
func NewEventBus(cfg BusConfig) (*EventBus, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("NATS URL is required")
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = DefaultMaxAge
	}
	if cfg.MaxMsgsPerSubject == 0 {
		cfg.MaxMsgsPerSubject = DefaultMaxMsgs
	}
	if cfg.ClientName == "" {
		cfg.ClientName = "gradient-api"
	}

	opts := []nats.Option{
		nats.Name(cfg.ClientName),
		nats.ReconnectWait(ReconnectWait),
		nats.MaxReconnects(MaxReconnects),
		nats.ReconnectBufSize(8 * 1024 * 1024), // 8MB reconnect buffer
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				log.Printf("[livectx] NATS disconnected: %v", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("[livectx] NATS reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("[livectx] NATS connection closed")
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			log.Printf("[livectx] NATS error: %v", err)
		}),
	}

	if cfg.AuthToken != "" {
		opts = append(opts, nats.Token(cfg.AuthToken))
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS at %s: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	bus := &EventBus{
		config:         cfg,
		conn:           nc,
		js:             js,
		consumers:      make(map[string]jetstream.ConsumeContext),
		publishLimiter: newRateLimiter(100, time.Second), // 100 events/second
	}

	// Provision the stream
	if err := bus.ensureStream(context.Background()); err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to provision JetStream stream: %w", err)
	}

	log.Printf("[livectx] Event bus connected to NATS at %s (stream: %s)", cfg.URL, StreamName)
	return bus, nil
}

// ensureStream creates or updates the JetStream stream.
func (b *EventBus) ensureStream(ctx context.Context) error {
	streamCfg := jetstream.StreamConfig{
		Name:              StreamName,
		Subjects:          []string{StreamSubjects},
		Retention:         jetstream.LimitsPolicy,
		MaxAge:            b.config.MaxAge,
		MaxMsgsPerSubject: b.config.MaxMsgsPerSubject,
		MaxMsgSize:        MaxEventSize,
		Storage:           jetstream.FileStorage,
		Discard:           jetstream.DiscardOld,
		Duplicates:        5 * time.Minute, // JetStream-level dedup window
		Replicas:          1,
	}

	stream, err := b.js.CreateOrUpdateStream(ctx, streamCfg)
	if err != nil {
		return fmt.Errorf("failed to create/update stream: %w", err)
	}

	b.stream = stream
	return nil
}

// Publish sends an event to the mesh.
// The event is validated, serialized, and published to the appropriate
// NATS subject based on org_id and branch.
func (b *EventBus) Publish(ctx context.Context, event *Event) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("event bus is closed")
	}
	b.mu.RUnlock()

	if err := event.Validate(); err != nil {
		return fmt.Errorf("event validation failed: %w", err)
	}

	// Rate limit
	if !b.publishLimiter.Allow() {
		return fmt.Errorf("publish rate limit exceeded (max %d/s)", b.publishLimiter.rate)
	}

	data, err := event.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if len(data) > MaxEventSize {
		return fmt.Errorf("event too large: %d bytes (max %d)", len(data), MaxEventSize)
	}

	subject := event.NATSSubject()

	// Publish with dedup via Msg-Id header
	msgID := event.ID
	if event.IdempotencyKey != "" {
		msgID = event.IdempotencyKey
	}

	ctx, cancel := context.WithTimeout(ctx, PublishTimeout)
	defer cancel()

	_, err = b.js.Publish(ctx, subject, data,
		jetstream.WithMsgID(msgID),
	)
	if err != nil {
		return fmt.Errorf("failed to publish event to %s: %w", subject, err)
	}

	return nil
}

// Subscribe starts consuming events for a given org+branch scope.
// The consumer is durable — it survives restarts and continues from
// where it left off. The handler is called for each received event.
// The consumerSuffix differentiates multiple consumers on the same subject
// (e.g. "agent-env-abc" or "api-server").
func (b *EventBus) Subscribe(ctx context.Context, orgID, branch, consumerSuffix string, handler EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return fmt.Errorf("event bus is closed")
	}

	subject := NATSSubject(orgID, branch)
	consumerName := fmt.Sprintf("%s-%s-%s", ConsumerDurable, sanitizeBranchForNATS(branch), consumerSuffix)

	// Create a durable consumer
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    5,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer %s: %w", consumerName, err)
	}

	// Start consuming
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		event, err := UnmarshalEvent(msg.Data())
		if err != nil {
			log.Printf("[livectx] Failed to unmarshal event from %s: %v", subject, err)
			_ = msg.Nak() // Negative ack — will be redelivered
			return
		}

		if err := handler(ctx, event); err != nil {
			log.Printf("[livectx] Handler error for event %s on %s: %v", event.ID, subject, err)
			_ = msg.NakWithDelay(5 * time.Second) // Retry with backoff
			return
		}

		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("failed to start consuming on %s: %w", subject, err)
	}

	b.consumers[subject] = cc
	b.handlers = append(b.handlers, handler)

	log.Printf("[livectx] Subscribed to %s (consumer: %s)", subject, consumerName)
	return nil
}

// SubscribeAll subscribes to all branches for a given org (wildcard).
// Useful for the API server to persist all events.
func (b *EventBus) SubscribeAll(ctx context.Context, orgID, consumerSuffix string, handler EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return fmt.Errorf("event bus is closed")
	}

	subject := NATSSubjectWildcard(orgID)
	consumerName := fmt.Sprintf("%s-all-%s", ConsumerDurable, consumerSuffix)

	consumer, err := b.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    5,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create wildcard consumer %s: %w", consumerName, err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		event, err := UnmarshalEvent(msg.Data())
		if err != nil {
			log.Printf("[livectx] Failed to unmarshal event: %v", err)
			_ = msg.Nak()
			return
		}

		if err := handler(ctx, event); err != nil {
			log.Printf("[livectx] Handler error for event %s: %v", event.ID, err)
			_ = msg.NakWithDelay(5 * time.Second)
			return
		}

		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("failed to start wildcard consumer: %w", err)
	}

	b.consumers[subject] = cc
	log.Printf("[livectx] Subscribed to all events for org %s (subject: %s)", orgID, subject)
	return nil
}

// Unsubscribe stops consuming events for a given org+branch.
func (b *EventBus) Unsubscribe(orgID, branch string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subject := NATSSubject(orgID, branch)
	if cc, ok := b.consumers[subject]; ok {
		cc.Stop()
		delete(b.consumers, subject)
		log.Printf("[livectx] Unsubscribed from %s", subject)
	}
}

// Close gracefully shuts down the event bus.
// It stops all consumers and drains the NATS connection.
func (b *EventBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	// Stop all consumers
	for subject, cc := range b.consumers {
		cc.Stop()
		log.Printf("[livectx] Stopped consumer for %s", subject)
	}
	b.consumers = make(map[string]jetstream.ConsumeContext)

	// Drain the connection (flush pending messages, then close)
	if b.conn != nil && !b.conn.IsClosed() {
		if err := b.conn.Drain(); err != nil {
			log.Printf("[livectx] Error draining NATS connection: %v", err)
			b.conn.Close()
		}
	}

	log.Printf("[livectx] Event bus closed")
	return nil
}

// IsConnected returns true if the NATS connection is active.
func (b *EventBus) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.conn != nil && b.conn.IsConnected() && !b.closed
}

// StreamInfo returns current stream information (for health checks / metrics).
func (b *EventBus) StreamInfo(ctx context.Context) (*StreamStatus, error) {
	if b.stream == nil {
		return nil, fmt.Errorf("stream not initialized")
	}

	info, err := b.stream.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream info: %w", err)
	}

	return &StreamStatus{
		Name:       info.Config.Name,
		Messages:   info.State.Msgs,
		Bytes:      info.State.Bytes,
		Consumers:  info.State.Consumers,
		FirstSeq:   info.State.FirstSeq,
		LastSeq:    info.State.LastSeq,
		MaxAge:     info.Config.MaxAge,
		Connected:  b.IsConnected(),
	}, nil
}

// StreamStatus represents the current state of the JetStream stream.
type StreamStatus struct {
	Name      string        `json:"name"`
	Messages  uint64        `json:"messages"`
	Bytes     uint64        `json:"bytes"`
	Consumers int           `json:"consumers"`
	FirstSeq  uint64        `json:"first_seq"`
	LastSeq   uint64        `json:"last_seq"`
	MaxAge    time.Duration `json:"max_age"`
	Connected bool          `json:"connected"`
}

// --- In-memory fallback for when NATS is not available ---

// LocalBus is an in-process event bus for development/testing
// when NATS is not configured. Events are broadcast to local handlers
// but not persisted or distributed.
type LocalBus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler // subject → handlers
	closed   bool
}

// NewLocalBus creates an in-process event bus for development.
func NewLocalBus() *LocalBus {
	return &LocalBus{
		handlers: make(map[string][]EventHandler),
	}
}

// Publish broadcasts an event to local handlers.
func (lb *LocalBus) Publish(ctx context.Context, event *Event) error {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if lb.closed {
		return fmt.Errorf("local bus is closed")
	}

	if err := event.Validate(); err != nil {
		return err
	}

	subject := event.NATSSubject()

	// Deliver to exact subject handlers
	for _, h := range lb.handlers[subject] {
		if err := h(ctx, event); err != nil {
			log.Printf("[livectx-local] Handler error: %v", err)
		}
	}

	// Deliver to wildcard handlers
	wildcard := NATSSubjectWildcard(event.OrgID)
	for _, h := range lb.handlers[wildcard] {
		if err := h(ctx, event); err != nil {
			log.Printf("[livectx-local] Wildcard handler error: %v", err)
		}
	}

	return nil
}

// Subscribe adds a handler for a specific subject.
func (lb *LocalBus) Subscribe(_ context.Context, orgID, branch, _ string, handler EventHandler) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	subject := NATSSubject(orgID, branch)
	lb.handlers[subject] = append(lb.handlers[subject], handler)
	return nil
}

// SubscribeAll adds a wildcard handler for all branches in an org.
func (lb *LocalBus) SubscribeAll(_ context.Context, orgID, _ string, handler EventHandler) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	subject := NATSSubjectWildcard(orgID)
	lb.handlers[subject] = append(lb.handlers[subject], handler)
	return nil
}

// Close shuts down the local bus.
func (lb *LocalBus) Close() error {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.closed = true
	lb.handlers = make(map[string][]EventHandler)
	return nil
}

// --- Rate limiter ---

type rateLimiter struct {
	mu       sync.Mutex
	rate     int           // max events per window
	window   time.Duration // window duration
	count    int           // current count in window
	windowAt time.Time     // start of current window
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		rate:     rate,
		window:   window,
		windowAt: time.Now(),
	}
}

func (rl *rateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.windowAt) >= rl.window {
		rl.count = 0
		rl.windowAt = now
	}

	if rl.count >= rl.rate {
		return false
	}

	rl.count++
	return true
}

// --- Bus interface for testability ---

// Bus is the interface that both EventBus and LocalBus implement.
type Bus interface {
	Publish(ctx context.Context, event *Event) error
	Subscribe(ctx context.Context, orgID, branch, consumerSuffix string, handler EventHandler) error
	SubscribeAll(ctx context.Context, orgID, consumerSuffix string, handler EventHandler) error
	Close() error
}

// Ensure both implementations satisfy Bus.
var (
	_ Bus = (*EventBus)(nil)
	_ Bus = (*LocalBus)(nil)
)

// --- Helper: Publish to bus AND persist to store ---

// MeshPublisher wraps a Bus and EventStore, ensuring events are both
// broadcast via NATS and durably persisted to PostgreSQL.
type MeshPublisher struct {
	bus   Bus
	store *EventStore
}

// NewMeshPublisher creates a publisher that writes to both bus and store.
func NewMeshPublisher(bus Bus, store *EventStore) *MeshPublisher {
	return &MeshPublisher{bus: bus, store: store}
}

// Publish sends an event to the bus and persists it to the store.
// Store persistence is best-effort — if it fails, the event is still
// broadcast (and can be persisted by the API server's wildcard consumer).
func (mp *MeshPublisher) Publish(ctx context.Context, event *Event) error {
	// Persist first (source of truth)
	if mp.store != nil {
		seq, err := mp.store.Publish(ctx, event)
		if err != nil {
			log.Printf("[livectx] Failed to persist event %s to store: %v", event.ID, err)
		} else {
			event.Sequence = seq
		}
	}

	// Broadcast
	if mp.bus != nil {
		if err := mp.bus.Publish(ctx, event); err != nil {
			log.Printf("[livectx] Failed to broadcast event %s: %v", event.ID, err)
			return err
		}
	}

	return nil
}

// --- Convenience constructors for typed events ---

// PublishPackageInstalled publishes a package_installed event.
func PublishPackageInstalled(ctx context.Context, pub *MeshPublisher, orgID, branch, envID, manager, name, version, command string) error {
	data := PackageData{
		Manager: manager,
		Name:    name,
		Version: version,
		Command: command,
	}
	event, err := NewEvent(EventPackageInstalled, orgID, branch, envID, data)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, event)
}

// PublishTestFailed publishes a test_failed event.
func PublishTestFailed(ctx context.Context, pub *MeshPublisher, orgID, branch, envID, test, errMsg, framework string, exitCode int) error {
	data := TestData{
		Test:      test,
		Error:     errMsg,
		Framework: framework,
		ExitCode:  exitCode,
	}
	event, err := NewEvent(EventTestFailed, orgID, branch, envID, data)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, event)
}

// PublishPatternLearned publishes a pattern_learned event.
func PublishPatternLearned(ctx context.Context, pub *MeshPublisher, orgID, branch, envID, key, value, category string) error {
	data := PatternData{
		Key:      key,
		Value:    value,
		Category: category,
	}
	event, err := NewEvent(EventPatternLearned, orgID, branch, envID, data)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, event)
}

// PublishConfigChanged publishes a config_changed event.
func PublishConfigChanged(ctx context.Context, pub *MeshPublisher, orgID, branch, envID, key, value, oldValue string) error {
	data := ConfigData{
		Key:      key,
		Value:    value,
		OldValue: oldValue,
	}
	event, err := NewEvent(EventConfigChanged, orgID, branch, envID, data)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, event)
}

// PublishErrorEncountered publishes an error_encountered event.
func PublishErrorEncountered(ctx context.Context, pub *MeshPublisher, orgID, branch, envID, errMsg, command string, exitCode int) error {
	data := ErrorData{
		Error:    errMsg,
		Command:  command,
		ExitCode: exitCode,
	}
	event, err := NewEvent(EventErrorEncountered, orgID, branch, envID, data)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, event)
}

// MarshalStreamStatus serializes stream status to JSON.
func (ss *StreamStatus) MarshalJSON() ([]byte, error) {
	type Alias StreamStatus
	return json.Marshal(&struct {
		MaxAge string `json:"max_age"`
		*Alias
	}{
		MaxAge: ss.MaxAge.String(),
		Alias:  (*Alias)(ss),
	})
}
