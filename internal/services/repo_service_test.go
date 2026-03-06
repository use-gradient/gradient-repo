package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-webhook-secret-123"

	// Create a RepoService with just the webhook secret (no DB needed for this test)
	svc := &RepoService{
		webhookSecret: secret,
	}

	// Helper to compute a valid signature
	computeSignature := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("valid signature accepted", func(t *testing.T) {
		body := []byte(`{"action":"push","ref":"refs/heads/main"}`)
		sig := computeSignature(body)

		if !svc.VerifyWebhookSignature(body, sig) {
			t.Error("Expected valid signature to be accepted")
		}
	})

	t.Run("invalid signature rejected", func(t *testing.T) {
		body := []byte(`{"action":"push","ref":"refs/heads/main"}`)

		if svc.VerifyWebhookSignature(body, "sha256=invalid") {
			t.Error("Expected invalid signature to be rejected")
		}
	})

	t.Run("tampered body rejected", func(t *testing.T) {
		originalBody := []byte(`{"action":"push"}`)
		sig := computeSignature(originalBody)

		tamperedBody := []byte(`{"action":"push","evil":true}`)
		if svc.VerifyWebhookSignature(tamperedBody, sig) {
			t.Error("Expected tampered body to be rejected")
		}
	})

	t.Run("empty signature rejected", func(t *testing.T) {
		body := []byte(`{"action":"push"}`)
		if svc.VerifyWebhookSignature(body, "") {
			t.Error("Expected empty signature to be rejected")
		}
	})

	t.Run("missing sha256 prefix rejected", func(t *testing.T) {
		body := []byte(`test`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sigWithoutPrefix := hex.EncodeToString(mac.Sum(nil))

		if svc.VerifyWebhookSignature(body, sigWithoutPrefix) {
			t.Error("Expected signature without sha256= prefix to be rejected")
		}
	})
}

func TestVerifyWebhookSignatureNoSecret(t *testing.T) {
	// When webhook secret is empty, all signatures should be accepted (dev mode)
	svc := &RepoService{
		webhookSecret: "",
	}

	body := []byte(`{"action":"push"}`)

	if !svc.VerifyWebhookSignature(body, "") {
		t.Error("Expected empty secret to accept all signatures")
	}
	if !svc.VerifyWebhookSignature(body, "sha256=anything") {
		t.Error("Expected empty secret to accept all signatures")
	}
}

func TestSnapshotStoreCreation(t *testing.T) {
	// Verify NewSnapshotStore doesn't panic with nil DB
	// (it shouldn't — it just stores the pointer)
	store := NewSnapshotStore(nil)
	if store == nil {
		t.Error("Expected non-nil SnapshotStore")
	}
}
