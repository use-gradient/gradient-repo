package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIEmbeddingProvider struct {
	apiKey     string
	model      string
	baseURL    string
	dimensions int
	client     *http.Client
}

func NewOpenAIEmbeddingProvider(apiKey, model, baseURL string, dimensions int) *OpenAIEmbeddingProvider {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if dimensions <= 0 {
		dimensions = 1536
	}
	return &OpenAIEmbeddingProvider{
		apiKey:     strings.TrimSpace(apiKey),
		model:      model,
		baseURL:    baseURL,
		dimensions: dimensions,
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *OpenAIEmbeddingProvider) ProviderName() string {
	return "openai"
}

func (p *OpenAIEmbeddingProvider) ModelName() string {
	return p.model
}

func (p *OpenAIEmbeddingProvider) Enabled() bool {
	return p.apiKey != ""
}

func (p *OpenAIEmbeddingProvider) EmbedTips(ctx context.Context, tips []TipEmbeddingInput) ([]Embedding, error) {
	if !p.Enabled() || len(tips) == 0 {
		return nil, nil
	}
	inputs := make([]string, 0, len(tips))
	for _, tip := range tips {
		inputs = append(inputs, tip.Text)
	}
	return p.embed(ctx, inputs)
}

func (p *OpenAIEmbeddingProvider) EmbedQuery(ctx context.Context, query RetrievalEmbeddingQuery) (Embedding, error) {
	if !p.Enabled() || strings.TrimSpace(query.Text) == "" {
		return Embedding{}, nil
	}
	items, err := p.embed(ctx, []string{query.Text})
	if err != nil {
		return Embedding{}, err
	}
	if len(items) == 0 {
		return Embedding{}, nil
	}
	return items[0], nil
}

func (p *OpenAIEmbeddingProvider) embed(ctx context.Context, input []string) ([]Embedding, error) {
	reqBody := map[string]interface{}{
		"model":           p.model,
		"input":           input,
		"encoding_format": "float",
		"dimensions":      p.dimensions,
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI embeddings request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI embeddings API %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI embeddings response: %w", err)
	}

	results := make([]Embedding, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		results = append(results, Embedding{
			Dimensions: len(item.Embedding),
			Values:     item.Embedding,
		})
	}
	return results, nil
}
