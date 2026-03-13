package services

import "testing"

func TestNewEmbeddingProviderOpenAI(t *testing.T) {
	provider := NewEmbeddingProvider("openai", "sk-test", "", "", 0)
	if provider == nil {
		t.Fatalf("expected provider")
	}
	if provider.ProviderName() != "openai" {
		t.Fatalf("expected openai provider, got %s", provider.ProviderName())
	}
	if provider.ModelName() != "text-embedding-3-small" {
		t.Fatalf("expected default model, got %s", provider.ModelName())
	}
	if !provider.Enabled() {
		t.Fatalf("expected provider to be enabled")
	}
}

func TestNewEmbeddingProviderFallback(t *testing.T) {
	provider := NewEmbeddingProvider("unknown", "", "", "", 0)
	if provider == nil {
		t.Fatalf("expected provider")
	}
	if provider.ProviderName() != "disabled" {
		t.Fatalf("expected disabled provider, got %s", provider.ProviderName())
	}
	if provider.Enabled() {
		t.Fatalf("expected disabled provider")
	}
}
