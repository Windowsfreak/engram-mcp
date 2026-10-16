package embedder

import (
	"context"
	"errors"
	"strings"

	"github.com/windowsfreak/engram-mcp/internal/chunker"
)

var (
	ErrEmptyInput         = errors.New("input text is empty")
	ErrDimensionsMismatch = errors.New("returned vector dimensions do not match expected")
	ErrProviderFailed     = errors.New("embedding provider failed")
)

const (
	// SearchQueryPrefix is the mandatory prefix for queries in nomic-embed-text-v2-moe.
	SearchQueryPrefix = "search_query: "

	// SearchDocumentPrefix is the mandatory prefix for documents in nomic-embed-text-v2-moe.
	SearchDocumentPrefix = "search_document: "
)

// Embedder defines the common interface for vector embedding generation.
type Embedder interface {
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	Model() string
}

// FormatQuery ensures the mandatory search_query: prefix is applied.
func FormatQuery(query string) string {
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(trimmed, SearchQueryPrefix) {
		return trimmed
	}
	return SearchQueryPrefix + trimmed
}

// FormatDocument ensures the mandatory search_document: prefix is applied.
func FormatDocument(docText string) string {
	trimmed := strings.TrimSpace(docText)
	if strings.HasPrefix(trimmed, SearchDocumentPrefix) {
		return trimmed
	}
	return SearchDocumentPrefix + trimmed
}

// ValidateTokenBudget checks whether the text is within the safe 512 token limit.
func ValidateTokenBudget(text string, maxTokens int) bool {
	return chunker.EstimateTokens(text) <= maxTokens
}
