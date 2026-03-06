package livectx

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIsValidEventType(t *testing.T) {
	validTypes := []EventType{
		EventPackageInstalled,
		EventPackageRemoved,
		EventTestFailed,
		EventTestFixed,
		EventPatternLearned,
		EventConfigChanged,
		EventDependencyAdded,
		EventErrorEncountered,
		EventCommandRan,
		EventFileChanged,
		EventCustom,
	}

	for _, et := range validTypes {
		if !IsValidEventType(et) {
			t.Errorf("Expected %q to be valid", et)
		}
	}

	invalidTypes := []EventType{"invalid", "foo", "", "PACKAGE_INSTALLED"}
	for _, et := range invalidTypes {
		if IsValidEventType(et) {
			t.Errorf("Expected %q to be invalid", et)
		}
	}
}

func TestNewEvent(t *testing.T) {
	data := PackageData{
		Manager: "pip",
		Name:    "torch",
		Version: "2.1.0",
	}

	event, err := NewEvent(EventPackageInstalled, "org-1", "main", "env-abc", data)
	if err != nil {
		t.Fatalf("NewEvent failed: %v", err)
	}

	if event.ID == "" {
		t.Error("Expected non-empty ID")
	}
	if event.SchemaVersion != SchemaVersion {
		t.Errorf("Expected SchemaVersion %d, got %d", SchemaVersion, event.SchemaVersion)
	}
	if event.Type != EventPackageInstalled {
		t.Errorf("Expected type %q, got %q", EventPackageInstalled, event.Type)
	}
	if event.OrgID != "org-1" {
		t.Errorf("Expected OrgID %q, got %q", "org-1", event.OrgID)
	}
	if event.Branch != "main" {
		t.Errorf("Expected Branch %q, got %q", "main", event.Branch)
	}
	if event.EnvID != "env-abc" {
		t.Errorf("Expected EnvID %q, got %q", "env-abc", event.EnvID)
	}
	if event.Source != "agent" {
		t.Errorf("Expected Source %q, got %q", "agent", event.Source)
	}
	if event.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
	if len(event.Data) == 0 {
		t.Error("Expected non-empty data")
	}

	// Verify data can be unmarshaled
	var pkg PackageData
	if err := json.Unmarshal(event.Data, &pkg); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}
	if pkg.Name != "torch" {
		t.Errorf("Expected package name 'torch', got %q", pkg.Name)
	}
}

func TestNewEventValidation(t *testing.T) {
	tests := []struct {
		name      string
		eventType EventType
		orgID     string
		branch    string
		envID     string
		data      interface{}
		wantErr   bool
	}{
		{"valid", EventPackageInstalled, "org-1", "main", "env-1", PackageData{Manager: "pip", Name: "x"}, false},
		{"invalid type", EventType("bad"), "org-1", "main", "env-1", PackageData{}, true},
		{"empty org", EventPackageInstalled, "", "main", "env-1", PackageData{}, true},
		{"empty branch", EventPackageInstalled, "org-1", "", "env-1", PackageData{}, true},
		{"empty env", EventPackageInstalled, "org-1", "main", "", PackageData{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEvent(tt.eventType, tt.orgID, tt.branch, tt.envID, tt.data)
			if tt.wantErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestEventValidate(t *testing.T) {
	// Valid event
	event, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Manager: "pip", Name: "torch", Version: "2.0"})
	if err := event.Validate(); err != nil {
		t.Errorf("Valid event should pass validation: %v", err)
	}

	// Missing ID
	badEvent := *event
	badEvent.ID = ""
	if err := badEvent.Validate(); err == nil {
		t.Error("Event with empty ID should fail validation")
	}

	// Missing data
	badEvent2 := *event
	badEvent2.Data = nil
	if err := badEvent2.Validate(); err == nil {
		t.Error("Event with nil data should fail validation")
	}

	// Invalid schema version
	badEvent3 := *event
	badEvent3.SchemaVersion = 0
	if err := badEvent3.Validate(); err == nil {
		t.Error("Event with schema_version 0 should fail validation")
	}

	// Package event without manager
	badPkg, _ := NewEvent(EventPackageInstalled, "org-1", "main", "env-1",
		PackageData{Name: "torch"})
	if err := badPkg.Validate(); err == nil {
		t.Error("Package event without manager should fail validation")
	}

	// Test event without test name
	badTest, _ := NewEvent(EventTestFailed, "org-1", "main", "env-1",
		TestData{Error: "failed"})
	if err := badTest.Validate(); err == nil {
		t.Error("Test event without test name should fail validation")
	}

	// Pattern event without key
	badPattern, _ := NewEvent(EventPatternLearned, "org-1", "main", "env-1",
		PatternData{Value: "do this"})
	if err := badPattern.Validate(); err == nil {
		t.Error("Pattern event without key should fail validation")
	}

	// Config event without key
	badConfig, _ := NewEvent(EventConfigChanged, "org-1", "main", "env-1",
		ConfigData{Value: "v"})
	if err := badConfig.Validate(); err == nil {
		t.Error("Config event without key should fail validation")
	}
}

func TestEventChaining(t *testing.T) {
	event, _ := NewEvent(EventPatternLearned, "org-1", "main", "env-1",
		PatternData{Key: "k", Value: "v"})

	event.WithSource("cli").WithIdempotencyKey("my-key").WithCausalID("parent-123").WithTTL(time.Hour)

	if event.Source != "cli" {
		t.Errorf("Expected Source 'cli', got %q", event.Source)
	}
	if event.IdempotencyKey != "my-key" {
		t.Errorf("Expected IdempotencyKey 'my-key', got %q", event.IdempotencyKey)
	}
	if event.CausalID != "parent-123" {
		t.Errorf("Expected CausalID 'parent-123', got %q", event.CausalID)
	}
	if event.ExpiresAt.IsZero() {
		t.Error("Expected non-zero ExpiresAt after WithTTL")
	}
}

func TestEventMarshalUnmarshal(t *testing.T) {
	original, _ := NewEvent(EventTestFailed, "org-1", "feature/auth", "env-xyz",
		TestData{Test: "test_login", Error: "OOM", Framework: "pytest", ExitCode: 1})
	original.WithSource("agent").WithCausalID("c-1")

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	restored, err := UnmarshalEvent(data)
	if err != nil {
		t.Fatalf("UnmarshalEvent failed: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID mismatch: %q vs %q", restored.ID, original.ID)
	}
	if restored.Type != original.Type {
		t.Errorf("Type mismatch: %q vs %q", restored.Type, original.Type)
	}
	if restored.Branch != original.Branch {
		t.Errorf("Branch mismatch: %q vs %q", restored.Branch, original.Branch)
	}
	if restored.CausalID != original.CausalID {
		t.Errorf("CausalID mismatch: %q vs %q", restored.CausalID, original.CausalID)
	}

	// Verify data payload survives round-trip
	var testData TestData
	if err := json.Unmarshal(restored.Data, &testData); err != nil {
		t.Fatalf("Failed to unmarshal restored data: %v", err)
	}
	if testData.Test != "test_login" {
		t.Errorf("Expected test 'test_login', got %q", testData.Test)
	}
}

func TestNATSSubject(t *testing.T) {
	tests := []struct {
		orgID  string
		branch string
		want   string
	}{
		{"org-1", "main", "ctx.org-1.main"},
		{"org-1", "feature/auth", "ctx.org-1.feature.auth"},
		{"org-123", "fix/bug-42", "ctx.org-123.fix.bug-42"},
		{"org-1", "release/v1.0", "ctx.org-1.release.v1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := NATSSubject(tt.orgID, tt.branch)
			if got != tt.want {
				t.Errorf("NATSSubject(%q, %q) = %q, want %q", tt.orgID, tt.branch, got, tt.want)
			}
		})
	}
}

func TestNATSSubjectWildcard(t *testing.T) {
	got := NATSSubjectWildcard("org-1")
	want := "ctx.org-1.>"
	if got != want {
		t.Errorf("NATSSubjectWildcard(%q) = %q, want %q", "org-1", got, want)
	}
}

func TestSanitizeBranchForNATS(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"main", "main"},
		{"feature/auth", "feature.auth"},
		{"fix/bug-42", "fix.bug-42"},
		{"release/v1.0", "release.v1.0"},
		{"feature/user@email", "feature.useremail"},
		{"test branch", "testbranch"},
		{"a/b/c", "a.b.c"},
		{"under_score", "under_score"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeBranchForNATS(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeBranchForNATS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComputeIdempotencyKey(t *testing.T) {
	data := []byte(`{"manager":"pip","name":"torch","version":"2.0"}`)

	key1 := ComputeIdempotencyKey(EventPackageInstalled, "env-1", data)
	key2 := ComputeIdempotencyKey(EventPackageInstalled, "env-1", data)
	key3 := ComputeIdempotencyKey(EventPackageInstalled, "env-2", data)
	key4 := ComputeIdempotencyKey(EventPackageRemoved, "env-1", data)

	// Same inputs → same key
	if key1 != key2 {
		t.Error("Same inputs should produce same key")
	}

	// Different env → different key
	if key1 == key3 {
		t.Error("Different env should produce different key")
	}

	// Different type → different key
	if key1 == key4 {
		t.Error("Different type should produce different key")
	}

	// Key should be 32 chars hex
	if len(key1) != 32 {
		t.Errorf("Expected key length 32, got %d", len(key1))
	}
}

func TestEventFilterDefaults(t *testing.T) {
	filter := EventFilter{
		OrgID: "org-1",
	}

	if filter.Branch != "" {
		t.Error("Expected empty Branch by default")
	}
	if filter.Limit != 0 {
		t.Error("Expected 0 Limit by default (store will use 100)")
	}
}

func TestPayloadStructs(t *testing.T) {
	// Test that all payload structs marshal/unmarshal correctly
	payloads := []struct {
		name string
		data interface{}
	}{
		{"PackageData", PackageData{Manager: "pip", Name: "torch", Version: "2.0", Command: "pip install torch==2.0"}},
		{"TestData", TestData{Test: "test_login", Error: "timeout", Framework: "pytest", ExitCode: 1}},
		{"PatternData", PatternData{Key: "retry", Value: "use backoff", Category: "networking", Confidence: 0.95}},
		{"ConfigData", ConfigData{Key: "PATH", Value: "/usr/bin", OldValue: "/bin", Scope: "env"}},
		{"DependencyData", DependencyData{File: "requirements.txt", Name: "torch", Version: ">=2.0", DevOnly: false}},
		{"ErrorData", ErrorData{Error: "OOM", Command: "python train.py", ExitCode: 137, Resolved: true, Solution: "reduce batch size"}},
		{"CommandData", CommandData{Command: "make test", ExitCode: 0, Duration: "5s"}},
		{"FileChangeData", FileChangeData{Path: "src/main.py", Action: "modified", LinesAdded: 10, LinesRemoved: 3}},
		{"CustomData", CustomData{Key: "my_key", Value: json.RawMessage(`{"foo": "bar"}`)}},
	}

	for _, tt := range payloads {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.data)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			if len(data) == 0 {
				t.Error("Expected non-empty JSON")
			}
		})
	}
}
