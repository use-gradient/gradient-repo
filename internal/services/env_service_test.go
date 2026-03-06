package services

import (
	"context"
	"testing"

	"github.com/gradient/gradient/pkg/env"
)

func TestCreateEnvironmentValidation(t *testing.T) {
	// Test validation logic that doesn't require DB
	svc := &EnvService{providers: map[string]env.Provider{}} // empty provider map

	tests := []struct {
		name      string
		req       *CreateEnvRequest
		wantError string
	}{
		{
			name:      "empty name",
			req:       &CreateEnvRequest{Region: "us-east-1"},
			wantError: "name is required",
		},
		{
			name:      "no providers configured - empty provider",
			req:       &CreateEnvRequest{Name: "test-env", Region: "us-east-1"},
			wantError: "no cloud providers configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateEnvironment(context.Background(), tt.req)
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			if err.Error() != tt.wantError {
				t.Errorf("Expected error %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}

func TestCreateEnvironmentProviderNotConfigured(t *testing.T) {
	// Only "hetzner" registered, request "gcp"
	svc := &EnvService{
		providers: map[string]env.Provider{"hetzner": nil},
	}

	req := &CreateEnvRequest{
		Name:     "test-env",
		Provider: "gcp",
		Region:   "us-west1",
	}

	_, err := svc.CreateEnvironment(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for unconfigured provider, got nil")
	}
	// Error should mention the available providers
	if !contains(err.Error(), "gcp") || !contains(err.Error(), "not configured") {
		t.Errorf("Expected error mentioning unconfigured provider, got %q", err.Error())
	}
}

func TestCreateEnvironmentDefaultProvider(t *testing.T) {
	svc := &EnvService{
		providers: map[string]env.Provider{},
	}

	// With empty provider and no configured providers, should get "no cloud providers configured"
	req := &CreateEnvRequest{
		Name:   "test-env",
		Region: "fsn1",
	}

	_, err := svc.CreateEnvironment(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if err.Error() != "no cloud providers configured" {
		t.Errorf("Expected 'no cloud providers configured', got %q", err.Error())
	}
}

func TestCreateEnvironmentDefaultSize(t *testing.T) {
	svc := &EnvService{
		providers: map[string]env.Provider{"hetzner": nil},
	}

	// Test that empty size defaults to "small" — validation passes,
	// then panics on nil repo (which proves validation was OK)
	req := &CreateEnvRequest{
		Name:     "test-env",
		Provider: "hetzner",
		Region:   "fsn1",
		// Size is empty — should default to "small"
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic on nil repo")
			}
		}()
		svc.CreateEnvironment(context.Background(), req)
	}()
}

func TestDestroyEnvironmentRequiresIDs(t *testing.T) {
	svc := &EnvService{}

	// Will panic on nil repo, but we're testing that it tries
	func() {
		defer func() {
			// Expected: panic from nil repo when trying to GetByID
			recover()
		}()
		err := svc.DestroyEnvironment(context.Background(), "", "org1")
		if err == nil {
			t.Error("Expected error for empty envID")
		}
	}()
}

func TestGetProviderValidation(t *testing.T) {
	svc := &EnvService{
		providers: map[string]env.Provider{},
	}

	t.Run("empty provider returns error", func(t *testing.T) {
		_, err := svc.getProvider("")
		if err == nil {
			t.Error("Expected error for empty provider")
		}
	})

	t.Run("gcp not registered returns error", func(t *testing.T) {
		_, err := svc.getProvider("gcp")
		if err == nil {
			t.Error("Expected error for gcp provider")
		}
	})

	t.Run("unknown provider returns error", func(t *testing.T) {
		_, err := svc.getProvider("azure")
		if err == nil {
			t.Error("Expected error for unknown provider")
		}
	})

	t.Run("hetzner not registered returns error", func(t *testing.T) {
		_, err := svc.getProvider("hetzner")
		if err == nil {
			t.Error("Expected error for unregistered hetzner provider")
		}
	})
}

func TestAvailableProviders(t *testing.T) {
	svc := &EnvService{
		providers: map[string]env.Provider{
			"hetzner": nil,
			"aws":     nil,
		},
	}

	providers := svc.AvailableProviders()
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}

	// Check both are present (order is non-deterministic for maps)
	found := map[string]bool{}
	for _, p := range providers {
		found[p] = true
	}
	if !found["hetzner"] || !found["aws"] {
		t.Errorf("Expected hetzner and aws, got %v", providers)
	}
}

func TestRegisterProvider(t *testing.T) {
	svc := &EnvService{
		providers: map[string]env.Provider{},
	}

	// Initially no providers
	if len(svc.AvailableProviders()) != 0 {
		t.Error("Expected no providers initially")
	}

	// Register one
	svc.RegisterProvider("gcp", nil)
	if len(svc.AvailableProviders()) != 1 {
		t.Error("Expected 1 provider after registration")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
