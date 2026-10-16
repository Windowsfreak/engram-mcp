# ARCHITECTURE STATE: Engram-MCP

This document provides a live, definitive architectural reference for **Engram-MCP**. It details system contracts, invariant rules, schema designs, and package responsibilities.

---

## 1. System Vision & Core Principles

Engram-MCP is an open-source, production-grade Knowledge & Agent Memory Engine written in idiomatic Go. It couples a human-accessible filesystem ground truth with a PostgreSQL + `pgvector` derived index and an MCP (Model Context Protocol) server.

### Three Foundational Principles:
1. **Filesystem-First Ground Truth:**
   - Raw Markdown notes, documents, and logs reside on disk under `/workspaces/{workspace_id}/`.
   - External tools (Obsidian, Copyparty, Syncthing, WebDAV, text editors) can freely edit and manage files.
   - Database state is a reactive index synchronized via file-hash diffing (SHA-256) and `fsnotify`.
2. **Mechanism Over Policy:**
   - The Go engine exposes orthogonal primitives: Virtual Filesystem (VFS) operations, parent-child chunk indexing, hybrid search, and instructions bootstrapping.
   - Organizational policy (e.g. `/atlas/`, `/episodes/`, `/graveyard/`, `/persona/`) is encoded in Markdown instruction templates (`AGENTS.md`), not hardcoded in Go tool schemas.
3. **Flexible Deployment Topologies:**
   - Runs locally (single-machine), hybrid (local vault + remote DB/NAS Ollama), or fully server-hosted (behind Caddy / Copyparty).

---

## 2. Invariants & Model Specifications

### 2.1 The `nomic-embed-text-v2-moe` Embedding Contract
- **Hard Context Limit:** 512 tokens.
- **Embedding Dimensions:** 768 float values.
- **Parent-Child Chunking Strategy:**
  - **Parent Section (`sections` table):** 1,200–2,500 tokens. Retains header hierarchy, code blocks, tables, and narrative context.
  - **Child Chunk (`chunks` table):** Strictly 250–400 tokens with a 50-token sliding overlap.
  - **Mandatory Task Prefixes:**
    - Every indexed child chunk must be prefixed with `search_document: ` before embedding.
    - Every search query must be prefixed with `search_query: ` before embedding.
  - **Query Expansion / Resolution:** When a child chunk matches in vector space, the parent section is retrieved and returned to the agent.

### 2.2 Hybrid Search with Reciprocal Rank Fusion (RRF)
Search combines:
1. **Semantic Vector Search:** Cosine distance (`<=>`) on `chunks.embedding`.
2. **Full-Text Search (FTS):** `ts_rank_cd` over `chunks.tsv` supporting language-specific dictionaries (`english`, `german`) and exact match (`simple`).
3. **Metadata & Temporal Filtering:** Deterministic SQL filters for `workspace_id`, path prefix (e.g., `/episodes/%`), YAML tags, and date ranges.
4. **Safety Boosting ("The Graveyard Guardrail"):** Path prefixes matching `/graveyard/` are evaluated alongside queries to ensure historical pitfalls and post-mortems are surfaced proactively.
5. **RRF Formula:**
   $$\text{RRF Score} = \frac{1}{k + \text{rank}_{\text{semantic}}} + \frac{1}{k + \text{rank}_{\text{lexical}}}$$
   with $k = 60$.

---

## 3. Package Responsibilities

```
cmd/engram/
├── main.go               # CLI entrypoint (stdio, http server, ingest, reindex, migrate)
internal/
├── config/               # Configuration structs, YAML parsing, environment overrides
├── db/                   # Connection pooling (pgx/v5), migration runner, schema types
├── vfs/                  # Filesystem operations, atomic writes, SHA-256 reconciliation, fsnotify watcher
├── chunker/              # Markdown/text parsing, token estimation, parent-child chunking
├── embedder/             # Embedder interface, Ollama & OpenAI clients, batching & retry
├── search/               # Hybrid RRF query construction, graveyard boosting, filter execution
└── mcp/                  # Official MCP protocol handlers (JSON-RPC 2.0 stdio + Streamable HTTP)
migrations/               # Raw SQL migration files (001_initial_schema.sql, ...)
docs/                     # Architecture & topology guides
```

---

## 4. Multi-Tenancy & Workspace Model

Every table in the database contains:
- `workspace_id TEXT NOT NULL DEFAULT 'default'`
- Primary keys and unique indices always include `(workspace_id, ...)`
- Root directories on disk are partitioned: `<data_dir>/workspaces/<workspace_id>/`
- API key authentication maps Bearer tokens or `stdio` parameters directly to a `workspace_id`.
