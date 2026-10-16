package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/config"
)

// OllamaEmbedder connects to a local or LAN Ollama instance.
type OllamaEmbedder struct {
	client     *http.Client
	baseURL    string
	model      string
	dimensions int
}

// NewOllamaEmbedder creates a new Ollama embedder.
func NewOllamaEmbedder(cfg config.OllamaConfig) *OllamaEmbedder {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	dims := cfg.Dimensions
	if dims <= 0 {
		dims = 768
	}

	model := cfg.Model
	if model == "" {
		model = "nomic-ai/nomic-embed-text-v2-moe"
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	return &OllamaEmbedder{
		client:     &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		model:      model,
		dimensions: dims,
	}
}

func (o *OllamaEmbedder) Dimensions() int {
	return o.dimensions
}

func (o *OllamaEmbedder) Model() string {
	return o.model
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

type ollamaLegacyRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaLegacyResponse struct {
	Embedding []float32 `json:"embedding"`
	Error     string    `json:"error,omitempty"`
}

// EmbedQuery embeds a single search query with the mandatory "search_query: " prefix.
func (o *OllamaEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	formatted := FormatQuery(query)
	results, err := o.EmbedDocuments(ctx, []string{formatted})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrProviderFailed
	}
	return results[0], nil
}

// EmbedDocuments embeds a slice of document texts, ensuring "search_document: " prefix.
func (o *OllamaEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	// Ensure prefix for all inputs
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = FormatDocument(t)
	}

	// Try modern /api/embed batch endpoint first
	embeddings, err := o.callEmbedEndpoint(ctx, prefixed)
	if err == nil {
		return embeddings, nil
	}

	// Fallback to legacy /api/embeddings endpoint (one by one)
	legacyResults := make([][]float32, len(prefixed))
	for i, text := range prefixed {
		vec, err := o.callLegacyEndpoint(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("ollama embedding failed for item %d: %w", i, err)
		}
		legacyResults[i] = vec
	}

	return legacyResults, nil
}

func (o *OllamaEmbedder) callEmbedEndpoint(ctx context.Context, inputs []string) ([][]float32, error) {
	endpoint := fmt.Sprintf("%s/api/embed", o.baseURL)
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: inputs,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama /api/embed returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed decoding ollama embed response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", res.Error)
	}

	return res.Embeddings, nil
}

func (o *OllamaEmbedder) callLegacyEndpoint(ctx context.Context, input string) ([]float32, error) {
	endpoint := fmt.Sprintf("%s/api/embeddings", o.baseURL)
	reqBody := ollamaLegacyRequest{
		Model:  o.model,
		Prompt: input,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama /api/embeddings returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ollamaLegacyResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed decoding legacy response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", res.Error)
	}

	return res.Embedding, nil
}
