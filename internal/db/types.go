package db

import (
	"encoding/json"
	"time"

	"github.com/pgvector/pgvector-go"
)

// Document represents a tracked file in a workspace.
type Document struct {
	WorkspaceID  string                 `json:"workspace_id"`
	Path         string                 `json:"path"`
	Title        string                 `json:"title"`
	SHA256       string                 `json:"sha256"`
	FileType     string                 `json:"file_type"`
	SizeBytes    int64                  `json:"size_bytes"`
	Frontmatter  map[string]interface{} `json:"frontmatter"`
	ModifiedAt   time.Time              `json:"modified_at"`
	IndexedAt    *time.Time             `json:"indexed_at,omitempty"`
	Status       string                 `json:"status"` // pending, indexing, indexed, error
	ErrorMessage *string                `json:"error_message,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// Section represents a parent chunk preserving complete Markdown/document context.
type Section struct {
	WorkspaceID      string    `json:"workspace_id"`
	ID               string    `json:"id"`
	DocumentPath     string    `json:"document_path"`
	SectionIndex     int       `json:"section_index"`
	Heading          string    `json:"heading"`
	HeadingLevel     int       `json:"heading_level"`
	Content          string    `json:"content"`
	TokenCount       int       `json:"token_count"`
	ByteOffsetStart  int       `json:"byte_offset_start"`
	ByteOffsetEnd    int       `json:"byte_offset_end"`
	CreatedAt        time.Time `json:"created_at"`
}

// Chunk represents an embeddable child fragment of a parent section.
type Chunk struct {
	WorkspaceID  string           `json:"workspace_id"`
	ID           string           `json:"id"`
	DocumentPath string           `json:"document_path"`
	SectionID    string           `json:"section_id"`
	ChunkIndex   int              `json:"chunk_index"`
	Content      string           `json:"content"`
	TokenCount   int              `json:"token_count"`
	Embedding    *pgvector.Vector `json:"-"`
	CreatedAt    time.Time        `json:"created_at"`
}

// EmbeddingJob represents a background task to compute vector embeddings.
type EmbeddingJob struct {
	ID           int64     `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	ChunkID      string    `json:"chunk_id"`
	DocumentPath string    `json:"document_path"`
	InputText    string    `json:"input_text"`
	Status       string    `json:"status"` // queued, processing, completed, failed
	Provider     string    `json:"provider"`
	Attempts     int       `json:"attempts"`
	MaxAttempts  int       `json:"max_attempts"`
	LastError    *string   `json:"last_error,omitempty"`
	NextRunAt    time.Time `json:"next_run_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// FrontmatterBytes serializes the frontmatter map to JSON bytes for SQL.
func (d *Document) FrontmatterBytes() ([]byte, error) {
	if d.Frontmatter == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(d.Frontmatter)
}
