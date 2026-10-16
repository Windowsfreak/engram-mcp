package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("record not found")
)

// DB wraps a PostgreSQL connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// New creates and initializes a new DB instance.
func New(ctx context.Context, connURL string, minConns, maxConns int32) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(connURL)
	if err != nil {
		return nil, fmt.Errorf("invalid db connection string: %w", err)
	}

	if minConns > 0 {
		poolConfig.MinConns = minConns
	}
	if maxConns > 0 {
		poolConfig.MaxConns = maxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Close closes all database connections in the pool.
func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
}

// Ping verifies that the database is reachable.
func (db *DB) Ping(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}

// UpsertDocument creates or updates a document record.
func (db *DB) UpsertDocument(ctx context.Context, doc *Document) error {
	fmBytes, err := doc.FrontmatterBytes()
	if err != nil {
		return fmt.Errorf("failed serializing frontmatter: %w", err)
	}

	query := `
		INSERT INTO documents (
			workspace_id, path, title, sha256, file_type, size_bytes,
			frontmatter, modified_at, indexed_at, status, error_message,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW()
		)
		ON CONFLICT (workspace_id, path) DO UPDATE SET
			title = EXCLUDED.title,
			sha256 = EXCLUDED.sha256,
			file_type = EXCLUDED.file_type,
			size_bytes = EXCLUDED.size_bytes,
			frontmatter = EXCLUDED.frontmatter,
			modified_at = EXCLUDED.modified_at,
			status = EXCLUDED.status,
			error_message = EXCLUDED.error_message,
			updated_at = NOW();
	`

	_, err = db.Pool.Exec(ctx, query,
		doc.WorkspaceID,
		doc.Path,
		doc.Title,
		doc.SHA256,
		doc.FileType,
		doc.SizeBytes,
		fmBytes,
		doc.ModifiedAt,
		doc.IndexedAt,
		doc.Status,
		doc.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by workspace and path.
func (db *DB) GetDocument(ctx context.Context, workspaceID, path string) (*Document, error) {
	query := `
		SELECT workspace_id, path, title, sha256, file_type, size_bytes,
		       frontmatter, modified_at, indexed_at, status, error_message,
		       created_at, updated_at
		FROM documents
		WHERE workspace_id = $1 AND path = $2
	`

	row := db.Pool.QueryRow(ctx, query, workspaceID, path)

	var doc Document
	var fmBytes []byte
	err := row.Scan(
		&doc.WorkspaceID,
		&doc.Path,
		&doc.Title,
		&doc.SHA256,
		&doc.FileType,
		&doc.SizeBytes,
		&fmBytes,
		&doc.ModifiedAt,
		&doc.IndexedAt,
		&doc.Status,
		&doc.ErrorMessage,
		&doc.CreatedAt,
		&doc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get document: %w", err)
	}

	if len(fmBytes) > 0 {
		var fm map[string]interface{}
		if err := json.Unmarshal(fmBytes, &fm); err == nil {
			doc.Frontmatter = fm
		}
	}

	return &doc, nil
}

// ListDocuments lists documents in a workspace, optionally filtered by path prefix.
func (db *DB) ListDocuments(ctx context.Context, workspaceID string, pathPrefix string) ([]*Document, error) {
	query := `
		SELECT workspace_id, path, title, sha256, file_type, size_bytes,
		       frontmatter, modified_at, indexed_at, status, error_message,
		       created_at, updated_at
		FROM documents
		WHERE workspace_id = $1 AND ($2 = '' OR path LIKE $2 || '%')
		ORDER BY path ASC
	`

	rows, err := db.Pool.Query(ctx, query, workspaceID, pathPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list documents: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		var doc Document
		var fmBytes []byte
		err := rows.Scan(
			&doc.WorkspaceID,
			&doc.Path,
			&doc.Title,
			&doc.SHA256,
			&doc.FileType,
			&doc.SizeBytes,
			&fmBytes,
			&doc.ModifiedAt,
			&doc.IndexedAt,
			&doc.Status,
			&doc.ErrorMessage,
			&doc.CreatedAt,
			&doc.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed scanning document: %w", err)
		}
		if len(fmBytes) > 0 {
			var fm map[string]interface{}
			if err := json.Unmarshal(fmBytes, &fm); err == nil {
				doc.Frontmatter = fm
			}
		}
		docs = append(docs, &doc)
	}

	return docs, rows.Err()
}

// DeleteDocument removes a document and cascades to its sections, chunks, and jobs.
func (db *DB) DeleteDocument(ctx context.Context, workspaceID, path string) error {
	query := `DELETE FROM documents WHERE workspace_id = $1 AND path = $2`
	_, err := db.Pool.Exec(ctx, query, workspaceID, path)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}
	return nil
}

// MoveDocument updates the document path and cascades to all child references.
func (db *DB) MoveDocument(ctx context.Context, workspaceID, oldPath, newPath string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Update document path (foreign keys on sections and chunks cascade UPDATE)
	_, err = tx.Exec(ctx, `
		UPDATE documents
		SET path = $1, updated_at = NOW()
		WHERE workspace_id = $2 AND path = $3
	`, newPath, workspaceID, oldPath)
	if err != nil {
		return fmt.Errorf("failed to update document path: %w", err)
	}

	// Update any queued embedding jobs
	_, err = tx.Exec(ctx, `
		UPDATE embedding_jobs
		SET document_path = $1, updated_at = NOW()
		WHERE workspace_id = $2 AND document_path = $3
	`, newPath, workspaceID, oldPath)
	if err != nil {
		return fmt.Errorf("failed to update jobs document path: %w", err)
	}

	return tx.Commit(ctx)
}

// UpdateDocumentStatus updates the indexing status and error message of a document.
func (db *DB) UpdateDocumentStatus(ctx context.Context, workspaceID, path, status string, errMsg *string) error {
	var query string
	if status == "indexed" {
		query = `
			UPDATE documents
			SET status = $1, error_message = $2, indexed_at = NOW(), updated_at = NOW()
			WHERE workspace_id = $3 AND path = $4
		`
	} else {
		query = `
			UPDATE documents
			SET status = $1, error_message = $2, updated_at = NOW()
			WHERE workspace_id = $3 AND path = $4
		`
	}

	_, err := db.Pool.Exec(ctx, query, status, errMsg, workspaceID, path)
	if err != nil {
		return fmt.Errorf("failed to update document status: %w", err)
	}
	return nil
}

// ReplaceDocumentSectionsAndChunks atomically replaces sections, chunks, and queues jobs.
func (db *DB) ReplaceDocumentSectionsAndChunks(
	ctx context.Context,
	workspaceID, docPath string,
	sections []*Section,
	chunks []*Chunk,
	jobs []*EmbeddingJob,
) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Delete existing sections for this document (cascades to chunks)
	_, err = tx.Exec(ctx, `
		DELETE FROM sections WHERE workspace_id = $1 AND document_path = $2
	`, workspaceID, docPath)
	if err != nil {
		return fmt.Errorf("failed to delete old sections: %w", err)
	}

	// Delete pending jobs for this document
	_, err = tx.Exec(ctx, `
		DELETE FROM embedding_jobs WHERE workspace_id = $1 AND document_path = $2
	`, workspaceID, docPath)
	if err != nil {
		return fmt.Errorf("failed to delete old embedding jobs: %w", err)
	}

	// Insert Sections
	for _, sec := range sections {
		_, err := tx.Exec(ctx, `
			INSERT INTO sections (
				workspace_id, id, document_path, section_index,
				heading, heading_level, content, token_count,
				byte_offset_start, byte_offset_end, created_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW()
			)
		`,
			sec.WorkspaceID, sec.ID, sec.DocumentPath, sec.SectionIndex,
			sec.Heading, sec.HeadingLevel, sec.Content, sec.TokenCount,
			sec.ByteOffsetStart, sec.ByteOffsetEnd,
		)
		if err != nil {
			return fmt.Errorf("failed to insert section %s: %w", sec.ID, err)
		}
	}

	// Insert Chunks
	for _, chk := range chunks {
		_, err := tx.Exec(ctx, `
			INSERT INTO chunks (
				workspace_id, id, document_path, section_id,
				chunk_index, content, token_count, embedding, created_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, NOW()
			)
		`,
			chk.WorkspaceID, chk.ID, chk.DocumentPath, chk.SectionID,
			chk.ChunkIndex, chk.Content, chk.TokenCount, chk.Embedding,
		)
		if err != nil {
			return fmt.Errorf("failed to insert chunk %s: %w", chk.ID, err)
		}
	}

	// Insert Embedding Jobs
	for _, job := range jobs {
		_, err := tx.Exec(ctx, `
			INSERT INTO embedding_jobs (
				workspace_id, chunk_id, document_path, input_text,
				status, provider, attempts, max_attempts, next_run_at,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW(), NOW()
			)
		`,
			job.WorkspaceID, job.ChunkID, job.DocumentPath, job.InputText,
			job.Status, job.Provider, job.Attempts, job.MaxAttempts,
		)
		if err != nil {
			return fmt.Errorf("failed to enqueue embedding job for chunk %s: %w", job.ChunkID, err)
		}
	}

	return tx.Commit(ctx)
}

// FetchPendingEmbeddingJobs retrieves batch of queued embedding jobs for processing.
func (db *DB) FetchPendingEmbeddingJobs(ctx context.Context, limit int) ([]*EmbeddingJob, error) {
	query := `
		UPDATE embedding_jobs
		SET status = 'processing', updated_at = NOW()
		WHERE id IN (
			SELECT id FROM embedding_jobs
			WHERE status = 'queued' AND next_run_at <= NOW()
			ORDER BY id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, workspace_id, chunk_id, document_path, input_text,
		          status, provider, attempts, max_attempts, last_error,
		          next_run_at, created_at, updated_at
	`

	rows, err := db.Pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*EmbeddingJob
	for rows.Next() {
		var j EmbeddingJob
		err := rows.Scan(
			&j.ID, &j.WorkspaceID, &j.ChunkID, &j.DocumentPath, &j.InputText,
			&j.Status, &j.Provider, &j.Attempts, &j.MaxAttempts, &j.LastError,
			&j.NextRunAt, &j.CreatedAt, &j.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed scanning job: %w", err)
		}
		jobs = append(jobs, &j)
	}

	return jobs, rows.Err()
}

// CompleteEmbeddingJob updates chunk embedding and marks the job as completed.
func (db *DB) CompleteEmbeddingJob(ctx context.Context, jobID int64, embedding []float32) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Find the job chunk info
	var workspaceID, chunkID, docPath string
	err = tx.QueryRow(ctx, `
		SELECT workspace_id, chunk_id, document_path
		FROM embedding_jobs
		WHERE id = $1
	`, jobID).Scan(&workspaceID, &chunkID, &docPath)
	if err != nil {
		return fmt.Errorf("job not found: %w", err)
	}

	vec := pgvector.NewVector(embedding)

	// Update chunk with embedding
	_, err = tx.Exec(ctx, `
		UPDATE chunks
		SET embedding = $1
		WHERE workspace_id = $2 AND id = $3
	`, vec, workspaceID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to update chunk embedding: %w", err)
	}

	// Delete completed job
	_, err = tx.Exec(ctx, `DELETE FROM embedding_jobs WHERE id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("failed to delete completed job: %w", err)
	}

	// Check if all chunks for this document are embedded
	var pendingCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM embedding_jobs
		WHERE workspace_id = $1 AND document_path = $2
	`, workspaceID, docPath).Scan(&pendingCount)
	if err == nil && pendingCount == 0 {
		// All chunks embedded, mark document as indexed
		_, _ = tx.Exec(ctx, `
			UPDATE documents
			SET status = 'indexed', indexed_at = NOW(), error_message = NULL, updated_at = NOW()
			WHERE workspace_id = $1 AND path = $2
		`, workspaceID, docPath)
	}

	return tx.Commit(ctx)
}

// FailEmbeddingJob increments attempt counter and reschedules with exponential backoff.
func (db *DB) FailEmbeddingJob(ctx context.Context, jobID int64, errMsg string, backoff time.Duration) error {
	query := `
		UPDATE embedding_jobs
		SET attempts = attempts + 1,
		    status = CASE WHEN attempts + 1 >= max_attempts THEN 'failed' ELSE 'queued' END,
		    last_error = $1,
		    next_run_at = NOW() + $2::interval,
		    updated_at = NOW()
		WHERE id = $3
	`

	backoffStr := fmt.Sprintf("%d seconds", int(backoff.Seconds()))
	_, err := db.Pool.Exec(ctx, query, errMsg, backoffStr, jobID)
	if err != nil {
		return fmt.Errorf("failed to update failed job: %w", err)
	}
	return nil
}
