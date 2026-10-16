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

func TestSidecarEmbedder(t *testing.T) {
	// Mock FastAPI/Uvicorn sidecar server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embed" {
			var req struct {
				Input []string `json:"input"`
				Model string   `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			// Verify prefix
			for _, inp := range req.Input {
				if !strings.HasPrefix(inp, "search_document: ") && !strings.HasPrefix(inp, "search_query: ") {
					http.Error(w, "missing required prefix", http.StatusBadRequest)
					return
				}
			}

			type item struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}
			var data []item
			for i := range req.Input {
				vec := make([]float32, 768)
				vec[0] = 0.5
				data = append(data, item{
					Index:     i,
					Embedding: vec,
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

	cfg := config.SidecarConfig{
		BaseURL:    server.URL,
		Model:      "nomic-ai/nomic-embed-text-v2-moe",
		Dimensions: 768,
		BatchSize:  16,
		TimeoutSec: 5,
	}

	emb := embedder.NewSidecarEmbedder(cfg)
	ctx := context.Background()

	// 1. Test Query embedding (with search_query: prefix)
	qVec, err := emb.EmbedQuery(ctx, "fast sidecar latency test")
	if err != nil {
		t.Fatalf("failed query embedding: %v", err)
	}
	if len(qVec) != 768 || qVec[0] != 0.5 {
		t.Errorf("unexpected query vector: %v", qVec[:5])
	}

	// 2. Test Documents embedding (with search_document: prefix)
	dVecs, err := emb.EmbedDocuments(ctx, []string{"chunk 1", "chunk 2"})
	if err != nil {
		t.Fatalf("failed documents embedding: %v", err)
	}
	if len(dVecs) != 2 || len(dVecs[0]) != 768 {
		t.Errorf("unexpected documents vectors count: %d", len(dVecs))
	}
}
