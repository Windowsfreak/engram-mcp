package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/db"
	"github.com/pgvector/pgvector-go"
)

// QueryEmbedder generates vector embeddings for search queries.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
}

// Engine implements the Hybrid RRF Search with graveyard safety boosting.
type Engine struct {
	db       *db.DB
	embedder QueryEmbedder
	cfg      *config.SearchConfig
}

// NewEngine creates a new search engine instance.
func NewEngine(database *db.DB, embedder QueryEmbedder, cfg *config.SearchConfig) *Engine {
	if cfg == nil {
		cfg = &config.SearchConfig{
			DefaultTopK:       5,
			RRFConstantK:      60,
			GraveyardPrefixes: []string{"/graveyard/", "/graveyard"},
		}
	}
	return &Engine{
		db:       database,
		embedder: embedder,
		cfg:      cfg,
	}
}

// Search executes a hybrid semantic + lexical search with Reciprocal Rank Fusion.
func (e *Engine) Search(ctx context.Context, params SearchParams) (*SearchResponse, error) {
	if params.TopK <= 0 {
		params.TopK = e.cfg.DefaultTopK
		if params.TopK <= 0 {
			params.TopK = 5
		}
	}
	if params.WorkspaceID == "" {
		params.WorkspaceID = "default"
	}

	trimmedQuery := strings.TrimSpace(params.Query)
	if trimmedQuery == "" {
		return &SearchResponse{
			Query:   params.Query,
			Results: []*SearchResult{},
		}, nil
	}

	// 1. Generate query embedding with mandatory "search_query: " prefix if embedder is available
	var queryVector *pgvector.Vector
	if e.embedder != nil {
		vec, err := e.embedder.EmbedQuery(ctx, trimmedQuery)
		if err == nil && len(vec) > 0 {
			v := pgvector.NewVector(vec)
			queryVector = &v
		}
	}

	// 2. Execute Hybrid RRF Query
	results, err := e.executeHybridQuery(ctx, params, queryVector, params.Paths, params.TopK)
	if err != nil {
		return nil, fmt.Errorf("hybrid search failed: %w", err)
	}

	response := &SearchResponse{
		Query:        params.Query,
		TotalResults: len(results),
		Results:      results,
	}

	// 3. Graveyard Guardrail: check for warnings in /graveyard/ if enabled
	if params.IncludeBoostedPaths && len(e.cfg.GraveyardPrefixes) > 0 {
		graveyardResults, err := e.executeHybridQuery(ctx, params, queryVector, e.cfg.GraveyardPrefixes, 3)
		if err == nil && len(graveyardResults) > 0 {
			// Filter out results that are already top results to avoid duplicate display
			existingIDs := make(map[string]bool)
			for _, r := range results {
				existingIDs[r.SectionID] = true
			}

			var warnings []*SearchResult
			for _, gr := range graveyardResults {
				gr.IsGraveyard = true
				if !existingIDs[gr.SectionID] {
					warnings = append(warnings, gr)
				}
			}
			response.GraveyardWarnings = warnings
		}
	}

	return response, nil
}

// executeHybridQuery constructs and executes the CTE RRF search.
func (e *Engine) executeHybridQuery(
	ctx context.Context,
	params SearchParams,
	queryVec *pgvector.Vector,
	pathFilters []string,
	limit int,
) ([]*SearchResult, error) {
	var args []interface{}
	argIdx := 1

	workspaceIDArg := argIdx
	args = append(args, params.WorkspaceID)
	argIdx++

	rawQueryArg := argIdx
	args = append(args, params.Query)
	argIdx++

	rrfK := e.cfg.RRFConstantK
	if rrfK <= 0 {
		rrfK = 60
	}
	rrfKArg := argIdx
	args = append(args, rrfK)
	argIdx++

	limitArg := argIdx
	args = append(args, limit)
	argIdx++

	var queryVecArg int
	hasVector := queryVec != nil
	if hasVector {
		queryVecArg = argIdx
		args = append(args, queryVec)
		argIdx++
	}

	// Dynamic WHERE clauses for both semantic and lexical legs
	var filterClauses []string

	if len(pathFilters) > 0 {
		var pathConditions []string
		for _, p := range pathFilters {
			pathConditions = append(pathConditions, fmt.Sprintf("d.path LIKE $%d || '%%'", argIdx))
			args = append(args, p)
			argIdx++
		}
		filterClauses = append(filterClauses, "("+strings.Join(pathConditions, " OR ")+")")
	}

	if params.DateFrom != nil {
		filterClauses = append(filterClauses, fmt.Sprintf("d.modified_at >= $%d", argIdx))
		args = append(args, *params.DateFrom)
		argIdx++
	}

	if params.DateTo != nil {
		filterClauses = append(filterClauses, fmt.Sprintf("d.modified_at <= $%d", argIdx))
		args = append(args, *params.DateTo)
		argIdx++
	}

	if len(params.Tags) > 0 {
		tagsJSON, _ := json.Marshal(params.Tags)
		filterClauses = append(filterClauses, fmt.Sprintf("d.frontmatter->'tags' @> $%d::jsonb", argIdx))
		args = append(args, string(tagsJSON))
		argIdx++
	}

	extraFilters := ""
	if len(filterClauses) > 0 {
		extraFilters = " AND " + strings.Join(filterClauses, " AND ")
	}

	// Build Semantic leg
	var semanticCTE string
	if hasVector {
		semanticCTE = fmt.Sprintf(`
		semantic_ranks AS (
			SELECT 
				c.workspace_id,
				c.section_id,
				MIN(c.embedding <=> $%d) AS min_dist,
				ROW_NUMBER() OVER (ORDER BY MIN(c.embedding <=> $%d) ASC) AS rank
			FROM chunks c
			JOIN documents d ON d.workspace_id = c.workspace_id AND d.path = c.document_path
			WHERE c.workspace_id = $%d
			  AND c.embedding IS NOT NULL
			  %s
			GROUP BY c.workspace_id, c.section_id
			ORDER BY min_dist ASC
			LIMIT 100
		)`, queryVecArg, queryVecArg, workspaceIDArg, extraFilters)
	} else {
		// Empty semantic leg when no vector is provided
		semanticCTE = `
		semantic_ranks AS (
			SELECT 
				''::text AS workspace_id,
				''::text AS section_id,
				0.0::float8 AS min_dist,
				1::int AS rank
			WHERE false
		)`
	}

	// Lexical leg using multilingual tsquery
	lexicalCTE := fmt.Sprintf(`
	lexical_ranks AS (
		SELECT 
			c.workspace_id,
			c.section_id,
			MAX(ts_rank_cd(c.tsv, plainto_tsquery('simple', $%d))) AS max_rank,
			ROW_NUMBER() OVER (ORDER BY MAX(ts_rank_cd(c.tsv, plainto_tsquery('simple', $%d))) DESC) AS rank
		FROM chunks c
		JOIN documents d ON d.workspace_id = c.workspace_id AND d.path = c.document_path
		WHERE c.workspace_id = $%d
		  AND c.tsv @@ (
			  plainto_tsquery('english', $%d) ||
			  plainto_tsquery('german', $%d) ||
			  plainto_tsquery('simple', $%d)
		  )
		  %s
		GROUP BY c.workspace_id, c.section_id
		ORDER BY max_rank DESC
		LIMIT 100
	)`, rawQueryArg, rawQueryArg, workspaceIDArg, rawQueryArg, rawQueryArg, rawQueryArg, extraFilters)

	sqlQuery := fmt.Sprintf(`
	WITH %s,
	%s,
	rrf_scores AS (
		SELECT 
			COALESCE(s.workspace_id, l.workspace_id) AS workspace_id,
			COALESCE(s.section_id, l.section_id) AS section_id,
			CASE WHEN s.rank IS NOT NULL THEN (1.0 / ($%d + s.rank)) ELSE 0.0 END AS semantic_score,
			CASE WHEN l.rank IS NOT NULL THEN (1.0 / ($%d + l.rank)) ELSE 0.0 END AS lexical_score,
			(CASE WHEN s.rank IS NOT NULL THEN (1.0 / ($%d + s.rank)) ELSE 0.0 END +
			 CASE WHEN l.rank IS NOT NULL THEN (1.0 / ($%d + l.rank)) ELSE 0.0 END) AS rrf_score,
			s.min_dist,
			l.max_rank
		FROM semantic_ranks s
		FULL OUTER JOIN lexical_ranks l ON s.workspace_id = l.workspace_id AND s.section_id = l.section_id
		WHERE COALESCE(s.section_id, l.section_id) != ''
	)
	SELECT 
		sec.id AS section_id,
		sec.document_path,
		sec.heading,
		sec.heading_level,
		sec.content,
		sec.token_count,
		d.title AS doc_title,
		d.frontmatter,
		d.modified_at,
		r.rrf_score,
		r.semantic_score,
		r.lexical_score,
		r.min_dist,
		CASE 
			WHEN sec.document_path LIKE '/graveyard/%%' OR sec.document_path LIKE '/graveyard' THEN true 
			ELSE false 
		END AS is_graveyard
	FROM rrf_scores r
	JOIN sections sec ON sec.workspace_id = r.workspace_id AND sec.id = r.section_id
	JOIN documents d ON d.workspace_id = sec.workspace_id AND d.path = sec.document_path
	ORDER BY r.rrf_score DESC
	LIMIT $%d;
	`, semanticCTE, lexicalCTE, rrfKArg, rrfKArg, rrfKArg, rrfKArg, limitArg)

	rows, err := e.db.Pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("error executing rrf search query: %w", err)
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		var sr SearchResult
		var fmBytes []byte
		var minDistance *float64

		err := rows.Scan(
			&sr.SectionID,
			&sr.DocumentPath,
			&sr.Heading,
			&sr.HeadingLevel,
			&sr.Content,
			&sr.TokenCount,
			&sr.DocumentTitle,
			&fmBytes,
			&sr.ModifiedAt,
			&sr.RRFScore,
			&sr.SemanticScore,
			&sr.LexicalScore,
			&minDistance,
			&sr.IsGraveyard,
		)
		if err != nil {
			return nil, fmt.Errorf("error scanning search result row: %w", err)
		}

		sr.MinDistance = minDistance
		if len(fmBytes) > 0 {
			var fm map[string]interface{}
			if err := json.Unmarshal(fmBytes, &fm); err == nil {
				sr.Frontmatter = fm
			}
		}

		results = append(results, &sr)
	}

	return results, rows.Err()
}
