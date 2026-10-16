package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/config"
)

// OpenAIEmbedder connects to OpenAI-compatible embedding endpoints (including freellmapi).
type OpenAIEmbedder struct {
	client     *http.Client
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	batchSize  int
	sem        chan struct{}
	mu         sync.Mutex
}

// NewOpenAIEmbedder creates a new OpenAI-compatible embedder.
func NewOpenAIEmbedder(cfg config.OpenAIConfig) *OpenAIEmbedder {
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
		model = "text-embedding-3-small"
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 32
	}

	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	return &OpenAIEmbedder{
		client:     &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		dimensions: dims,
		batchSize:  batchSize,
		sem:        make(chan struct{}, concurrency),
	}
}

func (o *OpenAIEmbedder) Dimensions() int {
	return o.dimensions
}

func (o *OpenAIEmbedder) Model() string {
	return o.model
}

type openAIEmbedRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

type openAIEmbedData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type openAIEmbedResponse struct {
	Data  []openAIEmbedData `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// EmbedQuery embeds a search query.
func (o *OpenAIEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
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

// EmbedDocuments embeds texts in batches.
func (o *OpenAIEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = FormatDocument(t)
	}

	allEmbeddings := make([][]float32, len(prefixed))

	// Process in batches
	for i := 0; i < len(prefixed); i += o.batchSize {
		end := i + o.batchSize
		if end > len(prefixed) {
			end = len(prefixed)
		}

		batch := prefixed[i:end]
		batchEmbeddings, err := o.callBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("openai batch embedding failed: %w", err)
		}

		for j, emb := range batchEmbeddings {
			allEmbeddings[i+j] = emb
		}
	}

	return allEmbeddings, nil
}

func (o *OpenAIEmbedder) callBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	// Acquire semaphore for concurrency control
	select {
	case o.sem <- struct{}{}:
		defer func() { <-o.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	endpoint := fmt.Sprintf("%s/embeddings", o.baseURL)
	reqBody := openAIEmbedRequest{
		Input: inputs,
		Model: o.model,
	}
	if o.dimensions > 0 && strings.Contains(o.model, "text-embedding-3") {
		dims := o.dimensions
		reqBody.Dimensions = &dims
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
	if o.apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.apiKey))
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai /embeddings returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed decoding openai embed response: %w", err)
	}

	if res.Error != nil {
		return nil, fmt.Errorf("openai error: %s", res.Error.Message)
	}

	ordered := make([][]float32, len(inputs))
	for _, item := range res.Data {
		if item.Index < len(ordered) {
			ordered[item.Index] = item.Embedding
		}
	}

	return ordered, nil
}
