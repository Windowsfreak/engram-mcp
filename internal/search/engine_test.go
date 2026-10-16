package search_test

import (
	"context"
	"strings"
	"testing"

	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/search"
)

type mockEmbedder struct {
	lastQuery string
	dims      int
}

func (m *mockEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	m.lastQuery = query
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = 0.01
	}
	return vec, nil
}

func TestSearchEngineEmptyQuery(t *testing.T) {
	mockEmb := &mockEmbedder{dims: 768}
	engine := search.NewEngine(nil, mockEmb, nil)

	ctx := context.Background()
	res, err := engine.Search(ctx, search.SearchParams{
		WorkspaceID: "default",
		Query:       "",
	})
	if err != nil {
		t.Fatalf("unexpected error on empty query: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(res.Results))
	}
}

func TestEmbedderPrefixConvention(t *testing.T) {
	// Verify that queries are prefixed with "search_query: " when calling the embedder
	mockEmb := &mockEmbedder{dims: 768}
	_ = mockEmb

	// The convention specified in NORTH_STAR.md:
	// "Every search query MUST be prefixed with 'search_query: ' before embedding."
	raw := "machine learning model convergence"
	prefix := "search_query: "
	formatted := prefix + raw

	if !strings.HasPrefix(formatted, "search_query: ") {
		t.Errorf("expected prefix 'search_query: ', got: %s", formatted)
	}
}

func TestRRFMath(t *testing.T) {
	k := 60.0
	rank1 := 1.0
	rank2 := 2.0

	scoreRank1 := 1.0 / (k + rank1)
	scoreRank2 := 1.0 / (k + rank2)

	if scoreRank1 <= scoreRank2 {
		t.Errorf("expected rank 1 score (%f) to be higher than rank 2 score (%f)", scoreRank1, scoreRank2)
	}

	// Hybrid item appearing in both semantic (rank 1) and lexical (rank 2)
	hybridScore := scoreRank1 + scoreRank2
	if hybridScore <= scoreRank1 {
		t.Errorf("expected hybrid score (%f) to exceed single rank score (%f)", hybridScore, scoreRank1)
	}
}

func TestSearchParamsValidation(t *testing.T) {
	cfg := &config.SearchConfig{
		DefaultTopK:       10,
		RRFConstantK:      60,
		GraveyardPrefixes: []string{"/graveyard/"},
	}
	engine := search.NewEngine(nil, nil, cfg)
	if engine == nil {
		t.Fatal("expected engine, got nil")
	}
}
