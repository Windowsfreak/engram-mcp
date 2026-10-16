package chunker

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/windowsfreak/engram-mcp/internal/db"
)

const (
	// SearchDocumentPrefix is the mandatory task prefix required by nomic-embed-text-v2-moe for indexing.
	SearchDocumentPrefix = "search_document: "

	// ChildChunkTargetTokens is the target token count for child chunks (between 250 and 400).
	ChildChunkTargetTokens = 320

	// ChildChunkOverlapTokens is the sliding window overlap between consecutive child chunks.
	ChildChunkOverlapTokens = 50
)

// ChunkResult contains the generated sections, child chunks, and embedding jobs.
type ChunkResult struct {
	Document *db.Document
	Sections []*db.Section
	Chunks   []*db.Chunk
	Jobs     []*db.EmbeddingJob
}

// Chunker handles parent-child document chunking.
type Chunker struct {
	defaultProvider string
}

// NewChunker creates a new Chunker instance.
func NewChunker(defaultProvider string) *Chunker {
	if defaultProvider == "" {
		defaultProvider = "ollama"
	}
	return &Chunker{defaultProvider: defaultProvider}
}

// ProcessDocument splits document content into parent sections and child chunks with embedding jobs.
func (c *Chunker) ProcessDocument(doc *db.Document, rawContent []byte) (*ChunkResult, error) {
	contentStr := strings.ReplaceAll(string(rawContent), "\r\n", "\n")

	// If CSV, convert to Markdown table first
	if doc.FileType == "csv" || strings.HasSuffix(strings.ToLower(doc.Path), ".csv") {
		converted, err := ConvertCSVToMarkdown(contentStr)
		if err == nil && converted != "" {
			contentStr = converted
		}
	}

	// Parse frontmatter
	parsed := ParseFrontmatter(contentStr)
	if doc.Title == "" && parsed.Title != "" {
		doc.Title = parsed.Title
	}
	if len(parsed.Frontmatter) > 0 {
		if doc.Frontmatter == nil {
			doc.Frontmatter = parsed.Frontmatter
		} else {
			for k, v := range parsed.Frontmatter {
				doc.Frontmatter[k] = v
			}
		}
	}

	// Split Markdown into parent sections (1,200–2,500 tokens)
	rawSections := ParseMarkdownSections(parsed.Body)
	if len(rawSections) == 0 {
		return &ChunkResult{
			Document: doc,
			Sections: []*db.Section{},
			Chunks:   []*db.Chunk{},
			Jobs:     []*db.EmbeddingJob{},
		}, nil
	}

	docHash := hashString(doc.Path)
	var sections []*db.Section
	var chunks []*db.Chunk
	var jobs []*db.EmbeddingJob

	for sIdx, rs := range rawSections {
		secID := fmt.Sprintf("%s:s%d", docHash, sIdx)
		section := &db.Section{
			WorkspaceID:  doc.WorkspaceID,
			ID:           secID,
			DocumentPath: doc.Path,
			SectionIndex: sIdx,
			Heading:      rs.Heading,
			HeadingLevel: rs.HeadingLevel,
			Content:      rs.Content,
			TokenCount:   rs.TokenCount,
		}
		sections = append(sections, section)

		// Split parent section into child chunks (250–400 tokens with 50-token overlap)
		childChunks := splitIntoChildChunks(rs.Content, ChildChunkTargetTokens, ChildChunkOverlapTokens)
		for cIdx, childText := range childChunks {
			chunkID := fmt.Sprintf("%s:c%d", secID, cIdx)
			chunkTokens := EstimateTokens(childText)

			chunk := &db.Chunk{
				WorkspaceID:  doc.WorkspaceID,
				ID:           chunkID,
				DocumentPath: doc.Path,
				SectionID:    secID,
				ChunkIndex:   cIdx,
				Content:      childText,
				TokenCount:   chunkTokens,
			}
			chunks = append(chunks, chunk)

			// Create embedding job with mandatory "search_document: " prefix
			job := &db.EmbeddingJob{
				WorkspaceID:  doc.WorkspaceID,
				ChunkID:      chunkID,
				DocumentPath: doc.Path,
				InputText:    SearchDocumentPrefix + childText,
				Status:       "queued",
				Provider:     c.defaultProvider,
				MaxAttempts:  5,
			}
			jobs = append(jobs, job)
		}
	}

	return &ChunkResult{
		Document: doc,
		Sections: sections,
		Chunks:   chunks,
		Jobs:     jobs,
	}, nil
}

// splitIntoChildChunks divides text into overlapping chunks of approximately targetTokens.
func splitIntoChildChunks(text string, targetTokens, overlapTokens int) []string {
	totalTokens := EstimateTokens(text)
	if totalTokens <= targetTokens+50 {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}

	// Approximate words per chunk
	// 1 token is ~0.75 words, so targetTokens tokens ~ targetTokens * 0.75 words
	targetWords := (targetTokens * 3) / 4
	overlapWords := (overlapTokens * 3) / 4
	stepWords := targetWords - overlapWords
	if stepWords <= 0 {
		stepWords = targetWords / 2
	}

	var chunks []string
	for start := 0; start < len(words); start += stepWords {
		end := start + targetWords
		if end > len(words) {
			end = len(words)
		}

		chunkWords := words[start:end]
		chunkText := strings.Join(chunkWords, " ")
		chunks = append(chunks, chunkText)

		if end == len(words) {
			break
		}
	}

	return chunks
}

func hashString(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))[:12]
}
