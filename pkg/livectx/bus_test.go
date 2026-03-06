package livectx

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLocalBusPublishSubscribe(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var received []*Event
	var mu sync.Mutex

	ctx := context.Background()

	// Subscribe
	err := bus.Subscribe(ctx, "org-1", "main", "test", func(_ context.Context, event *Event) error {
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Publish
	event, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch", Version: "2.0"})

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("Expected 1 received event, got %d", len(received))
	}
	if received[0].ID != event.ID {
		t.Errorf("Event ID mismatch: %q vs %q", received[0].ID, event.ID)
	}
}

func TestLocalBusDifferentBranches(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var mainEvents, featureEvents []*Event
	var mu sync.Mutex

	ctx := context.Background()

	// Subscribe to main
	bus.Subscribe(ctx, "org-1", "main", "test-main", func(_ context.Context, event *Event) error {
		mu.Lock()
		mainEvents = append(mainEvents, event)
		mu.Unlock()
		return nil
	})

	// Subscribe to feature/auth
	bus.Subscribe(ctx, "org-1", "feature/auth", "test-feature", func(_ context.Context, event *Event) error {
		mu.Lock()
		featureEvents = append(featureEvents, event)
		mu.Unlock()
		return nil
	})

	// Publish to main
	mainEvent, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch"})
	bus.Publish(ctx, mainEvent)

	// Publish to feature/auth
	featureEvent, _ := NewEvent(EventTestFailed, "org-1", "feature/auth", "env-2",
		TestData{Test: "test_login", Error: "timeout"})
	bus.Publish(ctx, featureEvent)

	mu.Lock()
	defer mu.Unlock()

	if len(mainEvents) != 1 {
		t.Errorf("Expected 1 main event, got %d", len(mainEvents))
	}
	if len(featureEvents) != 1 {
		t.Errorf("Expected 1 feature event, got %d", len(featureEvents))
	}
}

func TestLocalBusSubscribeAll(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var allEvents []*Event
	var mu sync.Mutex

	ctx := context.Background()

	// Subscribe to all events for org-1
	bus.SubscribeAll(ctx, "org-1", "test-all", func(_ context.Context, event *Event) error {
		mu.Lock()
		allEvents = append(allEvents, event)
		mu.Unlock()
		return nil
	})

	// Publish to different branches
	e1, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch"})
	e2, _ := NewEvent(EventTestFailed, "org-1", "feature/auth", "env-2",
		TestData{Test: "test_login", Error: "timeout"})

	bus.Publish(ctx, e1)
	bus.Publish(ctx, e2)

	mu.Lock()
	defer mu.Unlock()

	if len(allEvents) != 2 {
		t.Errorf("Expected 2 events from SubscribeAll, got %d", len(allEvents))
	}
}

func TestLocalBusClose(t *testing.T) {
	bus := NewLocalBus()
	bus.Close()

	event, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch"})

	err := bus.Publish(context.Background(), event)
	if err == nil {
		t.Error("Expected error publishing to closed bus")
	}
}

func TestLocalBusValidation(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	// Publish invalid event
	event := &Event{} // empty event
	err := bus.Publish(context.Background(), event)
	if err == nil {
		t.Error("Expected validation error for empty event")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, 100*time.Millisecond)

	// First 3 should be allowed
	for i := 0; i < 3; i++ {
		if !rl.Allow() {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th should be denied
	if rl.Allow() {
		t.Error("4th request should be denied")
	}

	// Wait for window to reset
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	if !rl.Allow() {
		t.Error("Request after window reset should be allowed")
	}
}

func TestBusInterface(t *testing.T) {
	// Verify LocalBus satisfies Bus interface
	var _ Bus = (*LocalBus)(nil)

	// Test through interface
	var bus Bus = NewLocalBus()
	defer bus.Close()

	event, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch"})

	if err := bus.Publish(context.Background(), event); err != nil {
		t.Errorf("Publish through interface failed: %v", err)
	}
}

func TestMeshPublisherLocalOnly(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	// MeshPublisher without store (bus-only mode)
	pub := NewMeshPublisher(bus, nil)

	var received []*Event
	var mu sync.Mutex

	ctx := context.Background()

	bus.Subscribe(ctx, "org-1", "main", "test", func(_ context.Context, event *Event) error {
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		return nil
	})

	event, _ := NewEvent(EventPatternLearned, "org-1", "main", "env-1",
		PatternData{Key: "retry", Value: "use backoff"})

	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("MeshPublisher.Publish failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Errorf("Expected 1 received event, got %d", len(received))
	}
}
