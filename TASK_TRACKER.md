# TASK TRACKER: Engram-MCP

This document tracks execution progress across all phases defined in [NORTH_STAR.md](file:///Users/bjoern/projects/bjoern/engram-mcp/NORTH_STAR.md). It is updated atomically after every task and test verification.

---

## Progress Overview

- [x] **Phase 0: Scaffolding & State Tracking**
  - [x] Initialize Go module (`github.com/windowsfreak/engram-mcp`)
  - [x] Create modular package directory structure
  - [x] Create `TASK_TRACKER.md`
  - [x] Create `ARCHITECTURE_STATE.md`
  - [x] Create comprehensive `README.md`
  - [x] Setup `.gitignore`
  - [x] Verify baseline compilation (`go build ./...`)
  *(Completed 2026-10-16)*
- [x] **Phase 1: PostgreSQL + `pgvector` Schema & Hybrid RRF Search Engine**
  - [x] Write SQL migrations (`001_initial_schema.sql`): `documents`, `sections`, `chunks`, `embedding_jobs` with `workspace_id` multi-tenancy
  - [x] Implement database pool & migration runner in `internal/db` with `jackc/pgx/v5` and `pgvector/pgvector-go`
  - [x] Implement Hybrid RRF (Reciprocal Rank Fusion) search query builder in `internal/search`
  - [x] Add `/graveyard/` safety boosting and temporal/metadata filters to search engine
  - [x] Unit & query generation tests for `internal/db` and `internal/search`
  *(Completed 2026-10-16)*
- [x] **Phase 2: Reactive VFS Watcher, Document Parsers & Parent-Child Chunker**
  - [x] Implement filesystem-first storage layer in `internal/vfs` (atomic writes, path normalization, safe deletes to `.trash/`, SHA-256 diffing)
  - [x] Implement reactive filesystem watcher using `fsnotify/fsnotify` with debounce and reconciliation
  - [x] Implement Markdown section & frontmatter parser with token estimation
  - [x] Implement tabular/plain-text parsers (CSV, plain text, structured extraction stubs for PDF/DOCX)
  - [x] Implement Parent-Child Chunker (250–400 token child chunks, 1,200–2,500 token parent sections, mandatory `search_document: ` prefix)
  - [x] Unit tests for VFS, parsers, and chunker
  *(Completed 2026-10-16)*
- [x] **Phase 3: Resilient Async Embedding Worker Pool**
  - [x] Define embedder interface (`internal/embedder`) with batching & token validation
  - [x] Implement Ollama embedder (`nomic-ai/nomic-embed-text-v2-moe`, 768 dimensions)
  - [x] Implement OpenAI-compatible embedder (`freellmapi` / custom endpoints) with exponential backoff & rate limiting
  - [x] Implement persistent PostgreSQL embedding job queue and background worker pool
  - [x] Unit tests with HTTP mock servers for both Ollama and OpenAI providers
  *(Completed 2026-10-16)*
- [x] **Phase 4: The 8-Tool CRUD & Search MCP Server (`stdio` + HTTP) & Bootstrap Engine**
  - [x] Implement standard MCP protocol server supporting JSON-RPC 2.0 over `stdio` and streamable HTTP (SSE / POST)
  - [x] Implement tool 1: `engram_search`
  - [x] Implement tool 2: `engram_ls`
  - [x] Implement tool 3: `engram_read`
  - [x] Implement tool 4: `engram_write`
  - [x] Implement tool 5: `engram_patch`
  - [x] Implement tool 6: `engram_mv_rm`
  - [x] Implement tool 7: `engram_ingest`
  - [x] Implement tool 8: `engram_bootstrap`
  - [x] Create canonical template `/persona/agent_rules.md` (for `AGENTS.md` / `GEMINI.md` / `CLAUDE.md`)
  - [x] Complete unit & integration tests for all 8 MCP tools
  *(Completed 2026-10-16)*
- [x] **Phase 5: Deployment Recipes & Open-Source Polish**
  - [x] Create `docker-compose.yml` (`pgvector`, Ollama optional profile, engram-mcp)
  - [x] Create `Caddyfile.example` for reverse proxy with auth and HTTPS
  - [x] Create `docs/TOPOLOGIES.md` (Local All-in-One, Split Home/Lab, Zero-UI Cloud with Copyparty)
  - [x] Write CLI flags and config loader (`internal/config`) with YAML / environment variables
  - [x] Wire up `cmd/engram` entrypoint (supporting `serve-stdio`, `serve-http`, `ingest`, `reindex`, `migrate`)
  - [x] End-to-end integration tests and validation
  *(Completed 2026-10-16)*
- [ ] **Phase 6: Future / Optional Backlog**
  - [ ] Lightweight web search portal & device auth
