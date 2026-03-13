package services

import "context"

type TipEmbeddingInput struct {
	TipID string
	Text  string
}

type RetrievalEmbeddingQuery struct {
	Text string
}

type Embedding struct {
	Dimensions int
	Values     []float32
}

type EmbeddingProvider interface {
	ProviderName() string
	ModelName() string
	Enabled() bool
	EmbedTips(ctx context.Context, tips []TipEmbeddingInput) ([]Embedding, error)
	EmbedQuery(ctx context.Context, query RetrievalEmbeddingQuery) (Embedding, error)
}

type NullEmbeddingProvider struct{}

// NewEmbeddingProvider picks the provider from config/env-derived values.
// Future providers can be added here without changing the memory services.
func NewEmbeddingProvider(providerName, apiKey, model, baseURL string, dimensions int) EmbeddingProvider {
	if providerName == "" && apiKey != "" {
		providerName = "openai"
	}
	switch providerName {
	case "", "disabled", "none":
		return NewNullEmbeddingProvider()
	case "openai":
		return NewOpenAIEmbeddingProvider(apiKey, model, baseURL, dimensions)
	default:
		// TODO: add additional providers here when we support them.
		return NewNullEmbeddingProvider()
	}
}

func NewNullEmbeddingProvider() *NullEmbeddingProvider {
	return &NullEmbeddingProvider{}
}

func (p *NullEmbeddingProvider) ProviderName() string {
	return "disabled"
}

func (p *NullEmbeddingProvider) ModelName() string {
	return ""
}

func (p *NullEmbeddingProvider) Enabled() bool {
	return false
}

func (p *NullEmbeddingProvider) EmbedTips(ctx context.Context, tips []TipEmbeddingInput) ([]Embedding, error) {
	return nil, nil
}

func (p *NullEmbeddingProvider) EmbedQuery(ctx context.Context, query RetrievalEmbeddingQuery) (Embedding, error) {
	return Embedding{}, nil
}
