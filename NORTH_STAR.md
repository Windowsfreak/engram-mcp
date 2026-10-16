# PROJECT SPECIFICATION & PERMANENT NORTH STAR: "ENGRAM-MCP"
**Filesystem-First Hybrid Vector Memory Engine & CRUD MCP Server in Go**

---

## 0. META-INSTRUCTIONS FOR ALL AI AGENTS (READ FIRST)

You are building **Engram-MCP**, an open-source, production-grade, self-hosted Knowledge & Agent Memory Engine written in idiomatic Go (Golang).

### Rules for Iterative Execution Across Sessions (Model-Agnostic Continuity)
Because this project spans multiple iterations and may be executed by models of varying sizes and context limits, you MUST adhere to the following operational protocol in every session:
1. **State Tracking First:** Before writing any code, inspect `TASK_TRACKER.md` and `ARCHITECTURE_STATE.md` in the repository root. If they do not exist, create them as your very first action (Phase 0).
2. **Atomic Progress:** Never leave broken builds. Complete one modular task at a time, write unit/integration tests, verify compilation (`go build ./...` and `go test ./...`), and update `TASK_TRACKER.md` before finishing your turn.
3. **Mechanism Over Policy:** Keep the Go core strictly generic, modular, and SOLID. Domain workflows (such as how an agent formats logs or post-mortems) belong in configurable Markdown templates (`AGENTS.md`), NOT hardcoded into Go tool schemas.
4. **Lean Stack:** Use Go 1.24+ standard library wherever possible, paired with `jackc/pgx/v5`, `pgvector/pgvector-go`, `fsnotify/fsnotify`, and the official MCP protocol structures.

---

## 1. SYSTEM VISION & ARCHITECTURAL PHILOSOPHY

AI agents (Google Antigravity, Claude Code) excel at short-term tasks but lack durable, user-owned, long-term memory across years of research. Conversely, human note-taking apps (Obsidian) choke when flooded with thousands of machine-generated vector chunks from 500-page publications.

**Engram-MCP** solves this through three core design principles:
1. **Filesystem-First Ground Truth (Copyparty / Obsidian / WebDAV Ready):**
   * All human notes, logs, and raw source files (PDF, DOCX, CSV, XLS, MD) live in a normal, transparent directory tree on disk (`/workspaces/{workspace_id}/`).
   * Because the filesystem is the ground truth, users can edit notes locally in Obsidian or manage files remotely via existing tools like **Copyparty**, Nextcloud, Syncthing, or WebDAV without needing a custom-built file manager UI in v1.
   * PostgreSQL + `pgvector` acts as the high-speed derived index, parent-child chunk store, and hybrid search engine. Removing or editing a file on disk automatically updates the database via a reactive file watcher + content-hash reconciliation loop.
2. **Generic CRUD + Hybrid Search MCP Interface ("Mechanism Over Policy"):**
   * Instead of rigid, single-purpose tools, Engram-MCP exposes a powerful Virtual Filesystem (VFS) + Hybrid Search + Document Processing MCP interface.
   * How the agent organizes knowledge (e.g., `/atlas/`, `/episodes/`, `/graveyard/`, `/persona/`) is governed by instructions dynamically seeded into the project's `AGENTS.md` / `GEMINI.md` via a dedicated bootstrap tool.
3. **Flexible Deployment Topologies:**
   * Supports running 100% locally on a single machine, in a hybrid split (local files + remote DB/embedding server), or 100% server-hosted (paired with Copyparty/WebDAV and remote HTTP MCP).

---

## 2. CORE TECHNICAL SPECIFICATIONS

### 2.1 Storage & Workspace Isolation (Zero-Bloat Multi-Tenancy)
* Every database table includes `workspace_id TEXT NOT NULL DEFAULT 'default'`.
* Each `workspace_id` maps to a root directory on disk (e.g., `/data/workspaces/default/`).
* Authentication in v1 uses a lightweight, static API key mapping in `config.yaml` (mapping Bearer tokens or `stdio` flags to a specific `workspace_id`). No complex OAuth or user-management UI is built in v1, keeping the core lean while remaining 100% multi-tenant ready for future phases.

### 2.2 Embedding Engine & `nomic-embed-text-v2-moe` Rules
* **Pluggable Embedder Interface:** Supports local/LAN Ollama (`nomic-ai/nomic-embed-text-v2-moe`, 768 dims) and high-latency OpenAI-compatible batch APIs (`freellmapi`) backed by a persistent PostgreSQL job queue with rate-limiting and exponential backoff.
* **Strict 512-Token Ceiling:** Because `nomic-embed-text-v2-moe` has a hard 512-token context limit:
  * **Parent Sections (`sections` table):** 1,200–2,500 tokens of full context (preserving Markdown headers, code blocks, and tables).
  * **Child Chunks (`chunks` table):** Strictly 250–400 tokens (50-token overlap) referencing `section_id`.
* **Mandatory Task Prefixes:**
  * Every indexed child chunk MUST be prefixed with `"search_document: "` before embedding.
  * Every search query MUST be prefixed with `"search_query: "` before embedding.

### 2.3 Hybrid Search with Reciprocal Rank Fusion (RRF) & Safety Boosting
Implement a single-query PostgreSQL Hybrid Search combining:
1. **Semantic Leg:** `pgvector` HNSW cosine similarity (`<=>`) on child chunks, rolled up to deduplicated parent sections.
2. **Lexical Leg:** PostgreSQL Full-Text Search (`ts_rank_cd` over `tsvector`) supporting multilingual (`german`, `english`) and exact identifier (`simple`) matching.
3. **Metadata & Temporal Leg:** Hard SQL filtering on `workspace_id`, path prefixes (`path LIKE '/episodes/%'`), YAML frontmatter JSONB tags, and date ranges (`modified_at` / frontmatter `date`).
4. **Pinned Safety Boost (The Graveyard Guardrail):** Configurable path prefixes (defaulting to `["/graveyard/"]`) are automatically checked during search queries so relevant past failures and post-mortems are surfaced alongside primary search hits.

---

## 3. THE MCP INTERFACE SPECIFICATION (`stdio` & Streamable HTTP)

Expose a clean, orthogonal set of 8 MCP tools that give Antigravity or Claude Code complete mastery over the knowledge base:

1. **`engram_search`**
   * **Inputs:** `query` (string), `paths` (optional array of path prefixes, e.g., `["/atlas", "/episodes"]`), `tags` (optional array), `date_from` / `date_to` (optional ISO dates), `top_k` (default 5), `include_boosted_paths` (bool, default true for `/graveyard`).
   * **Output:** Ranked parent sections with citation paths, similarity/RRF scores, metadata, and any triggered `/graveyard` warnings.
2. **`engram_ls`**
   * **Inputs:** `path` (default `"/"`), `recursive` (bool), `pattern` (optional glob).
   * **Output:** Directory tree with file types, sizes, modification dates, indexing status (`indexed`, `pending`, `error`), and YAML frontmatter summaries.
3. **`engram_read`**
   * **Inputs:** `path`, `section_Or_line_range` (optional, e.g., `"L10-L120"` or `"## Methodology"`).
   * **Output:** Raw Markdown content (or extracted Markdown representation for ingested PDFs/DOCX/CSV).
4. **`engram_write`**
   * **Inputs:** `path`, `content` (Markdown with optional YAML frontmatter), `overwrite` (bool).
   * **Behavior:** Writes the `.md` file atomically to the workspace directory on disk and immediately queues it for parent-child chunking and vector indexing.
5. **`engram_patch`**
   * **Inputs:** `path`, `operation` (`"append"`, `"prepend"`, `"replace_section"`, `"replace_text"`), `target` (header or old text), `content`.
   * **Behavior:** Safely modifies existing documents or appends chronological entries to episodic logs without rewriting huge files.
6. **`engram_mv_rm`**
   * **Inputs:** `action` (`"move"`, `"delete"`), `source_path`, `dest_path` (for move).
   * **Behavior:** Moves/renames files on disk (updating DB paths without re-embedding unchanged chunks!) or moves deleted files to `.trash/` and removes them from the active index.
7. **`engram_ingest`**
   * **Inputs:** `path` (path to raw `.pdf`, `.docx`, `.xlsx`, `.csv`, or directory inside `/sources/`), `generate_summary_md` (bool, optional target path in `/atlas/`).
   * **Behavior:** Extracts structured text/tables from binary documents into the PostgreSQL `sections`/`chunks` index without cluttering the user's Markdown folders with thousands of chunk files. Optionally creates a single human-readable overview `.md` file linking to the source.
8. **`engram_bootstrap`**
   * **Inputs:** `target_type` (`"GEMINI.md"`, `"AGENTS.md"`, `"CLAUDE.md"`), `custom_rules` (optional string).
   * **Output:** Returns (or writes locally if running in local mode) the canonical agent instruction file configured for the workspace, teaching the agent the folder conventions (`/atlas`, `/episodes`, `/graveyard`, `/persona`, `/00_Inbox`), YAML frontmatter schemas, and workflow habits.

---

## 4. DEFAULT WORKSPACE TAXONOMY (SEEDED INTO `AGENTS.md`)

While the engine allows any folder structure, `engram_bootstrap` seeds workspaces with this default convention:
* `/00_Inbox/` — Staging area for unverified ideas or drafts requiring human review.
* `/atlas/` — Validated domain knowledge, methods, code templates, and paper syntheses.
* `/episodes/` — Chronological work logs (`YYYY-MM-DD_topic.md`) recording exact parameters, git commits, and session progress ("what we did 7 days ago").
* `/graveyard/` — Post-mortems of failed approaches, diverging statistical models, and frustrating dead ends (automatically boosted in `engram_search`).
* `/persona/` — Evolving notes on the user's preferences, coding style, preferred libraries, and `agent_rules.md` (the template used by `engram_bootstrap`).
* `/sources/` — Raw binary publications (500-page PDFs, DOCX, datasets) indexed into `pgvector` in the background.

---

## 5. OPEN-SOURCE GITHUB REPOSITORY DELIVERABLE

Package **Engram-MCP** as a flagship open-source project with crystal-clear documentation:
* **`README.md`:** Vision, architecture diagrams, why "Mechanism over Policy" (VFS + Hybrid Search + `AGENTS.md` bootstrapping) beats rigid memory plugins, and `nomic-embed-text-v2-moe` parent-child chunking details.
* **`docs/TOPOLOGIES.md`:** Step-by-step setup guides for:
  1. **Local All-in-One:** Local Obsidian folder + local Docker `pgvector` + Ollama.
  2. **Split Home/Lab Setup:** vServer (`pgvector` + Engram HTTP MCP) + 20-core NAS running Ollama (`nomic-embed-text-v2-moe`) + `freellmapi` fallback.
  3. **Zero-UI Server Cloud with Copyparty:** Mounting and managing the Engram workspace via **Copyparty** (Web UI + WebDAV) alongside Obsidian and Antigravity.

---

## 6. PHASED EXECUTION ROADMAP

Execute strictly in order, updating `TASK_TRACKER.md` after every verified step:

* **Phase 0: Scaffolding & State Tracking**
  * Initialize Go module, clean package structure (`cmd/engram`, `internal/config`, `internal/db`, `internal/vfs`, `internal/chunker`, `internal/embedder`, `internal/search`, `internal/mcp`, `migrations/`).
  * Write `TASK_TRACKER.md`, `ARCHITECTURE_STATE.md`, and the comprehensive `README.md`.
* **Phase 1: PostgreSQL + `pgvector` Schema & Hybrid RRF Search Engine**
  * Create SQL migrations with `workspace_id` isolation, `documents`, `sections` (parents), `chunks` (children with `vector(768)` + `tsvector`), and `embedding_jobs`.
  * Implement the Hybrid RRF SQL query with `/graveyard/` safety boosting and unit tests.
* **Phase 2: Reactive VFS Watcher, Document Parsers & Parent-Child Chunker**
  * Implement the filesystem-first storage layer (`internal/vfs`) with SHA-256 content-hash diffing and `fsnotify` watcher so edits via Obsidian or Copyparty sync automatically.
  * Implement the Markdown + PDF/DOCX/CSV parser and the 400-token child / 2,000-token parent chunker with `search_document: ` prefixing.
* **Phase 3: Resilient Async Embedding Worker Pool**
  * Implement the persistent PostgreSQL job queue and worker pool supporting Ollama (`nomic-embed-text-v2-moe`) and OpenAI-compatible high-latency providers (`freellmapi`) with backoff.
* **Phase 4: The 8-Tool CRUD & Search MCP Server (`stdio` + HTTP) & Bootstrap Engine**
  * Implement `engram_search`, `engram_ls`, `engram_read`, `engram_write`, `engram_patch`, `engram_mv_rm`, `engram_ingest`, and `engram_bootstrap`.
  * Craft the default `/persona/agent_rules.md` template that `engram_bootstrap` uses to seed `AGENTS.md` / `GEMINI.md`.
* **Phase 5: Deployment Recipes & Open-Source Polish**
  * Create `docker-compose.yml`, `Caddyfile.example`, Copyparty integration guide (`docs/TOPOLOGIES.md`), and end-to-end integration tests.
* **Phase 6 (Future / Optional Backlog): Lightweight Web Search Portal & Device Auth**
  * Deferred until Phases 0–5 are battle-tested in daily research workflows.