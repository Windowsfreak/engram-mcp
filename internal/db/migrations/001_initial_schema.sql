-- Engram-MCP Initial Schema Migration (Phase 1)
-- Multi-tenant Knowledge & Memory Storage with pgvector and Hybrid RRF

CREATE EXTENSION IF NOT EXISTS vector;

-- 1. Documents Table
-- Represents individual files on disk (/workspaces/{workspace_id}/...)
CREATE TABLE IF NOT EXISTS documents (
    workspace_id TEXT NOT NULL DEFAULT 'default',
    path TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL,
    file_type TEXT NOT NULL DEFAULT 'md',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    frontmatter JSONB NOT NULL DEFAULT '{}'::jsonb,
    modified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    indexed_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, path)
);

CREATE INDEX IF NOT EXISTS idx_documents_workspace_status ON documents(workspace_id, status);
CREATE INDEX IF NOT EXISTS idx_documents_workspace_path ON documents(workspace_id, path);
CREATE INDEX IF NOT EXISTS idx_documents_frontmatter_gin ON documents USING gin(frontmatter);

-- 2. Sections Table (Parent Level: 1,200 - 2,500 tokens)
-- Contains full context, Markdown headings, code blocks, and narrative flow
CREATE TABLE IF NOT EXISTS sections (
    workspace_id TEXT NOT NULL DEFAULT 'default',
    id TEXT NOT NULL,
    document_path TEXT NOT NULL,
    section_index INT NOT NULL,
    heading TEXT NOT NULL DEFAULT '',
    heading_level INT NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    token_count INT NOT NULL DEFAULT 0,
    byte_offset_start INT NOT NULL DEFAULT 0,
    byte_offset_end INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, document_path) 
        REFERENCES documents(workspace_id, path) 
        ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sections_workspace_doc ON sections(workspace_id, document_path);

-- 3. Chunks Table (Child Level: 250 - 400 tokens)
-- Vectors for nomic-embed-text-v2-moe (768 dimensions) + multilingual tsvector
CREATE TABLE IF NOT EXISTS chunks (
    workspace_id TEXT NOT NULL DEFAULT 'default',
    id TEXT NOT NULL,
    document_path TEXT NOT NULL,
    section_id TEXT NOT NULL,
    chunk_index INT NOT NULL,
    content TEXT NOT NULL,
    token_count INT NOT NULL DEFAULT 0,
    embedding vector(768),
    tsv tsvector GENERATED ALWAYS AS (
        to_tsvector('english'::regconfig, content) ||
        to_tsvector('german'::regconfig, content) ||
        to_tsvector('simple'::regconfig, content)
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, section_id) 
        REFERENCES sections(workspace_id, id) 
        ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chunks_workspace_section ON chunks(workspace_id, section_id);
CREATE INDEX IF NOT EXISTS idx_chunks_workspace_doc ON chunks(workspace_id, document_path);
CREATE INDEX IF NOT EXISTS idx_chunks_tsv_gin ON chunks USING gin(tsv);
CREATE INDEX IF NOT EXISTS idx_chunks_embedding_hnsw ON chunks USING hnsw (embedding vector_cosine_ops);

-- 4. Embedding Jobs Table (Resilient Async Queue)
CREATE TABLE IF NOT EXISTS embedding_jobs (
    id BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT 'default',
    chunk_id TEXT NOT NULL,
    document_path TEXT NOT NULL,
    input_text TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    provider TEXT NOT NULL DEFAULT 'ollama',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    last_error TEXT,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_jobs_status_next_run ON embedding_jobs(status, next_run_at);
CREATE INDEX IF NOT EXISTS idx_jobs_workspace_chunk ON embedding_jobs(workspace_id, chunk_id);
