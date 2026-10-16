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

// SidecarEmbedder connects to the high-performance FastAPI/Uvicorn embedding sidecar.
type SidecarEmbedder struct {
	client     *http.Client
	baseURL    string
	model      string
	dimensions int
	batchSize  int
}

// NewSidecarEmbedder creates a new sidecar embedder client.
func NewSidecarEmbedder(cfg config.SidecarConfig) *SidecarEmbedder {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
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
		baseURL = "http://localhost:8001"
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 64
	}

	// Optimize transport with connection pooling and keep-alive for sub-5ms latency
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	return &SidecarEmbedder{
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		baseURL:    baseURL,
		model:      model,
		dimensions: dims,
		batchSize:  batchSize,
	}
}

func (s *SidecarEmbedder) Dimensions() int {
	return s.dimensions
}

func (s *SidecarEmbedder) Model() string {
	return s.model
}

type sidecarEmbedRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type sidecarEmbedItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type sidecarEmbedResponse struct {
	Data []sidecarEmbedItem `json:"data"`
}

// EmbedQuery generates an embedding for a search query using the mandatory prefix.
func (s *SidecarEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	formatted := FormatQuery(query)
	results, err := s.EmbedDocuments(ctx, []string{formatted})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrProviderFailed
	}
	return results[0], nil
}

// EmbedDocuments generates embeddings for multiple document chunks in batches.
func (s *SidecarEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	// Ensure prefix for all inputs
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = FormatDocument(t)
	}

	allEmbeddings := make([][]float32, len(prefixed))

	// Process in batches
	for i := 0; i < len(prefixed); i += s.batchSize {
		end := i + s.batchSize
		if end > len(prefixed) {
			end = len(prefixed)
		}

		batch := prefixed[i:end]
		batchEmbeddings, err := s.callBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("sidecar batch embedding failed: %w", err)
		}

		for j, emb := range batchEmbeddings {
			allEmbeddings[i+j] = emb
		}
	}

	return allEmbeddings, nil
}

func (s *SidecarEmbedder) callBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	endpoint := fmt.Sprintf("%s/embed", s.baseURL)
	reqBody := sidecarEmbedRequest{
		Input: inputs,
		Model: s.model,
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

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sidecar returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res sidecarEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed decoding sidecar response: %w", err)
	}

	ordered := make([][]float32, len(inputs))
	for _, item := range res.Data {
		if item.Index < len(ordered) {
			if len(item.Embedding) != s.dimensions {
				return nil, fmt.Errorf("%w: got %d, expected %d", ErrDimensionsMismatch, len(item.Embedding), s.dimensions)
			}
			ordered[item.Index] = item.Embedding
		}
	}

	return ordered, nil
}
