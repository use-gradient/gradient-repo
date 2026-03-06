package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gradient/gradient/internal/config"
)

func TestDeviceAuthStore(t *testing.T) {
	store := NewDeviceAuthStore()

	t.Run("create and get", func(t *testing.T) {
		entry := store.Create()
		if entry.Code == "" {
			t.Error("Expected non-empty code")
		}
		if entry.UserCode == "" {
			t.Error("Expected non-empty user code")
		}
		if entry.Status != "pending" {
			t.Errorf("Expected status 'pending', got %q", entry.Status)
		}

		got := store.Get(entry.Code)
		if got == nil {
			t.Fatal("Expected to find entry by code")
		}
		if got.Code != entry.Code {
			t.Errorf("Expected code %q, got %q", entry.Code, got.Code)
		}
	})

	t.Run("get nonexistent returns nil", func(t *testing.T) {
		got := store.Get("nonexistent-code")
		if got != nil {
			t.Error("Expected nil for nonexistent code")
		}
	})

	t.Run("get by user code", func(t *testing.T) {
		entry := store.Create()
		got := store.GetByUserCode(entry.UserCode)
		if got == nil {
			t.Fatal("Expected to find entry by user code")
		}
		if got.Code != entry.Code {
			t.Errorf("Code mismatch")
		}
	})

	t.Run("get by user code case insensitive", func(t *testing.T) {
		entry := store.Create()
		got := store.GetByUserCode("  " + entry.UserCode + "  ")
		if got == nil {
			t.Fatal("Expected to find entry with whitespace")
		}
	})

	t.Run("complete flow", func(t *testing.T) {
		entry := store.Create()

		ok := store.Complete(entry.Code, "user_123", "org_456", "test@example.com", "Test User", "tok_abc")
		if !ok {
			t.Fatal("Expected Complete to return true")
		}

		got := store.Get(entry.Code)
		if got.Status != "completed" {
			t.Errorf("Expected status 'completed', got %q", got.Status)
		}
		if got.Token != "tok_abc" {
			t.Errorf("Expected token 'tok_abc', got %q", got.Token)
		}
		if got.OrgID != "org_456" {
			t.Errorf("Expected org_id 'org_456', got %q", got.OrgID)
		}
	})

	t.Run("complete nonexistent returns false", func(t *testing.T) {
		ok := store.Complete("fake-code", "u", "o", "", "", "t")
		if ok {
			t.Error("Expected Complete to return false for nonexistent code")
		}
	})
}

func TestUserCodeFormat(t *testing.T) {
	// Generate many codes and verify format
	for i := 0; i < 100; i++ {
		code := generateUserCode()
		if len(code) != 9 { // XXXX-XXXX
			t.Errorf("Expected user code length 9, got %d: %q", len(code), code)
		}
		if code[4] != '-' {
			t.Errorf("Expected dash at position 4: %q", code)
		}
	}
}

func TestSecureCodeLength(t *testing.T) {
	code := generateSecureCode(32)
	if len(code) != 64 { // hex encoding doubles the length
		t.Errorf("Expected 64 char hex string, got %d: %q", len(code), code)
	}
}

func TestDeviceAuthEndpoints(t *testing.T) {
	s := &Server{
		config:     &config.Config{Env: "development"},
		deviceAuth: NewDeviceAuthStore(),
	}

	t.Run("start device auth", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/auth/device", nil)
		rec := httptest.NewRecorder()

		s.handleDeviceAuthStart(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		var result map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &result)

		if result["device_code"] == "" {
			t.Error("Expected non-empty device_code")
		}
		if result["user_code"] == "" {
			t.Error("Expected non-empty user_code")
		}
		if result["verification_url"] == "" {
			t.Error("Expected non-empty verification_url")
		}
		if result["expires_in"].(float64) != 600 {
			t.Error("Expected expires_in=600")
		}
	})

	t.Run("poll pending", func(t *testing.T) {
		// Create an entry
		entry := s.deviceAuth.Create()

		req := httptest.NewRequest("GET", "/api/v1/auth/device/poll?code="+entry.Code, nil)
		rec := httptest.NewRecorder()

		s.handleDeviceAuthPoll(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("Expected 202, got %d", rec.Code)
		}

		var result map[string]string
		json.Unmarshal(rec.Body.Bytes(), &result)
		if result["status"] != "pending" {
			t.Errorf("Expected status 'pending', got %q", result["status"])
		}
	})

	t.Run("poll completed", func(t *testing.T) {
		entry := s.deviceAuth.Create()
		s.deviceAuth.Complete(entry.Code, "u1", "o1", "u1@test.com", "User One", "token123")

		req := httptest.NewRequest("GET", "/api/v1/auth/device/poll?code="+entry.Code, nil)
		rec := httptest.NewRecorder()

		s.handleDeviceAuthPoll(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		var result map[string]string
		json.Unmarshal(rec.Body.Bytes(), &result)
		if result["status"] != "completed" {
			t.Errorf("Expected status 'completed', got %q", result["status"])
		}
		if result["token"] != "token123" {
			t.Errorf("Expected token 'token123', got %q", result["token"])
		}
	})

	t.Run("poll missing code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/auth/device/poll", nil)
		rec := httptest.NewRecorder()

		s.handleDeviceAuthPoll(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}
	})

	t.Run("poll invalid code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/auth/device/poll?code=bad", nil)
		rec := httptest.NewRecorder()

		s.handleDeviceAuthPoll(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", rec.Code)
		}
	})

	t.Run("approve in dev mode", func(t *testing.T) {
		entry := s.deviceAuth.Create()

		body, _ := json.Marshal(map[string]string{"code": entry.Code})
		req := httptest.NewRequest("POST", "/auth/cli/approve", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		s.handleDeviceAuthApprove(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		// Verify it's now completed
		got := s.deviceAuth.Get(entry.Code)
		if got.Status != "completed" {
			t.Errorf("Expected status 'completed', got %q", got.Status)
		}
		if got.Token == "" {
			t.Error("Expected non-empty auto-generated token in dev mode")
		}
		if got.OrgID != "dev-org" {
			t.Errorf("Expected org_id 'dev-org', got %q", got.OrgID)
		}
	})

	t.Run("approve invalid code", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"code": "fake"})
		req := httptest.NewRequest("POST", "/auth/cli/approve", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		s.handleDeviceAuthApprove(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", rec.Code)
		}
	})

	t.Run("auth page renders", func(t *testing.T) {
		entry := s.deviceAuth.Create()

		req := httptest.NewRequest("GET", "/auth/cli?code="+entry.Code, nil)
		rec := httptest.NewRecorder()

		s.handleAuthPage(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected HTML content type, got %q", ct)
		}
		body := rec.Body.String()
		if len(body) < 100 {
			t.Error("Expected HTML page content")
		}
	})

	t.Run("auth page missing code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/cli", nil)
		rec := httptest.NewRecorder()

		s.handleAuthPage(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", rec.Code)
		}
	})

	t.Run("auth page invalid code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/auth/cli?code=invalid", nil)
		rec := httptest.NewRecorder()

		s.handleAuthPage(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", rec.Code)
		}
	})

	t.Run("full flow end-to-end", func(t *testing.T) {
		// 1. CLI starts the flow
		startReq := httptest.NewRequest("POST", "/api/v1/auth/device", nil)
		startRec := httptest.NewRecorder()
		s.handleDeviceAuthStart(startRec, startReq)

		var startResult map[string]interface{}
		json.Unmarshal(startRec.Body.Bytes(), &startResult)
		deviceCode := startResult["device_code"].(string)

		// 2. CLI polls — should be pending
		pollReq := httptest.NewRequest("GET", "/api/v1/auth/device/poll?code="+deviceCode, nil)
		pollRec := httptest.NewRecorder()
		s.handleDeviceAuthPoll(pollRec, pollReq)
		if pollRec.Code != http.StatusAccepted {
			t.Fatalf("Expected 202, got %d", pollRec.Code)
		}

		// 3. Browser approves
		approveBody, _ := json.Marshal(map[string]string{"code": deviceCode})
		approveReq := httptest.NewRequest("POST", "/auth/cli/approve", bytes.NewReader(approveBody))
		approveReq.Header.Set("Content-Type", "application/json")
		approveRec := httptest.NewRecorder()
		s.handleDeviceAuthApprove(approveRec, approveReq)
		if approveRec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", approveRec.Code)
		}

		// 4. CLI polls again — should be completed
		pollReq2 := httptest.NewRequest("GET", "/api/v1/auth/device/poll?code="+deviceCode, nil)
		pollRec2 := httptest.NewRecorder()
		s.handleDeviceAuthPoll(pollRec2, pollReq2)
		if pollRec2.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", pollRec2.Code)
		}

		var finalResult map[string]string
		json.Unmarshal(pollRec2.Body.Bytes(), &finalResult)
		if finalResult["status"] != "completed" {
			t.Errorf("Expected 'completed', got %q", finalResult["status"])
		}
		if finalResult["token"] == "" {
			t.Error("Expected non-empty token")
		}
	})
}

func TestDeviceAuthExpiry(t *testing.T) {
	store := &DeviceAuthStore{
		codes: make(map[string]*DeviceAuthEntry),
	}

	// Create an entry that's already expired
	entry := &DeviceAuthEntry{
		Code:      "expired-code",
		UserCode:  "TEST-CODE",
		Status:    "pending",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // already expired
		CreatedAt: time.Now().Add(-11 * time.Minute),
	}
	store.codes["expired-code"] = entry

	got := store.Get("expired-code")
	if got != nil {
		t.Error("Expected nil for expired code")
	}

	ok := store.Complete("expired-code", "u", "o", "", "", "t")
	if ok {
		t.Error("Expected Complete to return false for expired code")
	}
}
