package search

import (
	"time"
)

// SearchParams contains all input parameters for an engram_search query.
type SearchParams struct {
	WorkspaceID         string     `json:"workspace_id"`
	Query               string     `json:"query"`
	Paths               []string   `json:"paths,omitempty"`
	Tags                []string   `json:"tags,omitempty"`
	DateFrom            *time.Time `json:"date_from,omitempty"`
	DateTo              *time.Time `json:"date_to,omitempty"`
	TopK                int        `json:"top_k"`
	IncludeBoostedPaths bool       `json:"include_boosted_paths"`
}

// SearchResult represents a parent section retrieved and scored via Hybrid RRF.
type SearchResult struct {
	SectionID     string                 `json:"section_id"`
	DocumentPath  string                 `json:"document_path"`
	Heading       string                 `json:"heading"`
	HeadingLevel  int                    `json:"heading_level"`
	Content       string                 `json:"content"`
	TokenCount    int                    `json:"token_count"`
	DocumentTitle string                 `json:"document_title"`
	Frontmatter   map[string]interface{} `json:"frontmatter,omitempty"`
	ModifiedAt    time.Time              `json:"modified_at"`
	RRFScore      float64                `json:"rrf_score"`
	SemanticScore float64                `json:"semantic_score"`
	LexicalScore  float64                `json:"lexical_score"`
	MinDistance   *float64               `json:"min_distance,omitempty"`
	IsGraveyard   bool                   `json:"is_graveyard"`
}

// SearchResponse is the full output returned to the agent.
type SearchResponse struct {
	Query             string          `json:"query"`
	TotalResults      int             `json:"total_results"`
	Results           []*SearchResult `json:"results"`
	GraveyardWarnings []*SearchResult `json:"graveyard_warnings,omitempty"`
}
