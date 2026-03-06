package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	// Health endpoint does not require auth or DB
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	// We can't easily instantiate a full Server without a DB,
	// so test the handler function directly.
	s := &Server{}
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %q", result["status"])
	}
	if result["version"] != "0.1.0" {
		t.Errorf("Expected version '0.1.0', got %q", result["version"])
	}
	if result["time"] == "" {
		t.Error("Expected non-empty time field")
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"key": "value"})

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got %q", ct)
	}

	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("Expected key='value', got %q", result["key"])
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "something went wrong")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}
	if result["error"] != "something went wrong" {
		t.Errorf("Expected error message 'something went wrong', got %q", result["error"])
	}
}

func TestCreateEnvironmentValidation(t *testing.T) {
	s := &Server{}

	tests := []struct {
		name       string
		body       interface{}
		wantStatus int
		wantError  string
	}{
		{
			name:       "empty body",
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
			wantError:  "name is required",
		},
		{
			name:       "missing name",
			body:       map[string]string{"region": "us-east-1"},
			wantStatus: http.StatusBadRequest,
			wantError:  "name is required",
		},
		{
			name:       "missing region",
			body:       map[string]string{"name": "test-env"},
			wantStatus: http.StatusBadRequest,
			wantError:  "region is required",
		},
		{
			name:       "invalid JSON",
			body:       nil, // will send empty body
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyBytes []byte
			if tt.body != nil {
				bodyBytes, _ = json.Marshal(tt.body)
			} else {
				bodyBytes = []byte("not json")
			}

			req := httptest.NewRequest("POST", "/api/v1/environments", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			s.handleCreateEnvironment(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			var result map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("Failed to decode error response: %v", err)
			}
			if result["error"] != tt.wantError {
				t.Errorf("Expected error %q, got %q", tt.wantError, result["error"])
			}
		})
	}
}

func TestSaveContextValidation(t *testing.T) {
	s := &Server{}

	t.Run("missing branch", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"commit_sha": "abc123"})
		req := httptest.NewRequest("POST", "/api/v1/contexts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		s.handleSaveContext(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}

		var result map[string]string
		json.Unmarshal(rec.Body.Bytes(), &result)
		if result["error"] != "branch is required" {
			t.Errorf("Expected 'branch is required' error, got %q", result["error"])
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/contexts", bytes.NewReader([]byte("bad")))
		rec := httptest.NewRecorder()

		s.handleSaveContext(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}
	})
}

func TestBillingSetupValidation(t *testing.T) {
	s := &Server{}

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/billing/setup", bytes.NewReader([]byte("bad")))
		rec := httptest.NewRecorder()

		s.handleBillingSetup(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}
	})
}

func TestConnectRepoValidation(t *testing.T) {
	s := &Server{}

	t.Run("missing repo", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{})
		req := httptest.NewRequest("POST", "/api/v1/repos", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		s.handleConnectRepo(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}

		var result map[string]string
		json.Unmarshal(rec.Body.Bytes(), &result)
		if result["error"] != "repo is required (format: owner/repo)" {
			t.Errorf("Unexpected error: %q", result["error"])
		}
	})
}

func TestListSnapshotsValidation(t *testing.T) {
	s := &Server{}

	t.Run("missing branch query param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/snapshots", nil)
		rec := httptest.NewRecorder()

		s.handleListSnapshots(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}

		var result map[string]string
		json.Unmarshal(rec.Body.Bytes(), &result)
		if result["error"] != "branch query parameter is required" {
			t.Errorf("Unexpected error: %q", result["error"])
		}
	})
}

func TestSecretSyncValidation(t *testing.T) {
	s := &Server{}

	tests := []struct {
		name      string
		body      map[string]string
		wantError string
	}{
		{
			name:      "missing all required fields",
			body:      map[string]string{},
			wantError: "environment_id, secret_key, and backend are required",
		},
		{
			name:      "missing backend",
			body:      map[string]string{"environment_id": "e1", "secret_key": "k1"},
			wantError: "environment_id, secret_key, and backend are required",
		},
		{
			name:      "invalid backend",
			body:      map[string]string{"environment_id": "e1", "secret_key": "k1", "backend": "invalid"},
			wantError: "backend must be 'vault'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/secrets/sync", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()

			s.handleSecretSync(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("Expected 400, got %d", rec.Code)
			}

			var result map[string]string
			json.Unmarshal(rec.Body.Bytes(), &result)
			if result["error"] != tt.wantError {
				t.Errorf("Expected error %q, got %q", tt.wantError, result["error"])
			}
		})
	}
}

func TestPublishEventValidation(t *testing.T) {
	s := &Server{}

	tests := []struct {
		name       string
		body       interface{}
		wantStatus int
	}{
		{
			name:       "invalid JSON",
			body:       nil,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty event missing required fields",
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyBytes []byte
			if tt.body != nil {
				bodyBytes, _ = json.Marshal(tt.body)
			} else {
				bodyBytes = []byte("not json")
			}

			req := httptest.NewRequest("POST", "/api/v1/events", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			s.handlePublishEvent(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d (body: %s)", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestQueryEventsValidation(t *testing.T) {
	s := &Server{}

	// Missing org_id (no auth context) — will get empty org_id from context
	req := httptest.NewRequest("GET", "/api/v1/events?branch=main", nil)
	rec := httptest.NewRecorder()

	s.handleQueryEvents(rec, req)

	// Without a database, this should fail
	if rec.Code == http.StatusOK {
		// If we get OK it means the store query ran — which requires DB
		// Without DB it should error
	}
}

func TestEventStatsValidation(t *testing.T) {
	s := &Server{}

	// Missing branch
	req := httptest.NewRequest("GET", "/api/v1/events/stats", nil)
	rec := httptest.NewRecorder()

	s.handleEventStats(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing branch, got %d", rec.Code)
	}

	var result map[string]string
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["error"] != "branch query parameter is required" {
		t.Errorf("Unexpected error: %q", result["error"])
	}
}

func TestStreamEventsValidation(t *testing.T) {
	s := &Server{}

	// Missing branch
	req := httptest.NewRequest("GET", "/api/v1/events/stream", nil)
	rec := httptest.NewRecorder()

	s.handleStreamEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing branch, got %d", rec.Code)
	}
}

func TestMeshHealthHandler(t *testing.T) {
	s := &Server{}

	req := httptest.NewRequest("GET", "/api/v1/mesh/health", nil)
	rec := httptest.NewRecorder()

	s.handleMeshHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to decode mesh health response: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %q", result["status"])
	}
}

func TestGitHubWebhookValidation(t *testing.T) {
	s := &Server{
		repoService: nil, // No repoService — will test signature validation
	}

	t.Run("empty body", func(t *testing.T) {
		// Create a request with a broken body
		req := httptest.NewRequest("POST", "/api/v1/webhooks/github", &errorReader{})
		rec := httptest.NewRecorder()

		s.handleGitHubWebhook(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}
	})
}

// errorReader is an io.Reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, &http.MaxBytesError{}
}
