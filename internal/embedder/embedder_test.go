package embedder_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/embedder"
)

func TestFormatPrefixes(t *testing.T) {
	// Query prefix test
	q1 := embedder.FormatQuery("loss function divergence")
	if q1 != "search_query: loss function divergence" {
		t.Errorf("unexpected query formatting: %s", q1)
	}

	// Idempotency test
	q2 := embedder.FormatQuery("search_query: already formatted")
	if q2 != "search_query: already formatted" {
		t.Errorf("double prefix applied: %s", q2)
	}

	// Document prefix test
	d1 := embedder.FormatDocument("notes on kalman filter")
	if d1 != "search_document: notes on kalman filter" {
		t.Errorf("unexpected document formatting: %s", d1)
	}

	d2 := embedder.FormatDocument("search_document: already prefixed")
	if d2 != "search_document: already prefixed" {
		t.Errorf("double prefix applied: %s", d2)
	}
}

func TestOllamaEmbedder(t *testing.T) {
	// Mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			// Verify prefix
			for _, inp := range req.Input {
				if !strings.HasPrefix(inp, "search_document: ") && !strings.HasPrefix(inp, "search_query: ") {
					http.Error(w, "missing required prefix", http.StatusBadRequest)
					return
				}
			}

			resp := struct {
				Embeddings [][]float32 `json:"embeddings"`
			}{
				Embeddings: [][]float32{
					make([]float32, 768),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.OllamaConfig{
		BaseURL:    server.URL,
		Model:      "nomic-ai/nomic-embed-text-v2-moe",
		Dimensions: 768,
		TimeoutSec: 5,
	}

	emb := embedder.NewOllamaEmbedder(cfg)
	ctx := context.Background()

	// Test query embedding
	qVec, err := emb.EmbedQuery(ctx, "agent memory architecture")
	if err != nil {
		t.Fatalf("failed embedding query: %v", err)
	}
	if len(qVec) != 768 {
		t.Errorf("expected 768 dimensions, got %d", len(qVec))
	}

	// Test document embedding
	dVecs, err := emb.EmbedDocuments(ctx, []string{"document section content"})
	if err != nil {
		t.Fatalf("failed embedding doc: %v", err)
	}
	if len(dVecs) != 1 || len(dVecs[0]) != 768 {
		t.Errorf("unexpected doc embedding result: %+v", dVecs)
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	// Mock OpenAI server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-api-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			var req struct {
				Input []string `json:"input"`
				Model string   `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			type item struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}
			var data []item
			for i := range req.Input {
				data = append(data, item{
					Index:     i,
					Embedding: make([]float32, 768),
				})
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": data,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.OpenAIConfig{
		BaseURL:    server.URL,
		APIKey:     "test-api-key",
		Model:      "text-embedding-3-small",
		Dimensions: 768,
		BatchSize:  2,
		TimeoutSec: 5,
	}

	emb := embedder.NewOpenAIEmbedder(cfg)
	ctx := context.Background()

	vecs, err := emb.EmbedDocuments(ctx, []string{"doc1", "doc2", "doc3"})
	if err != nil {
		t.Fatalf("failed embedding batch: %v", err)
	}
	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}
}
