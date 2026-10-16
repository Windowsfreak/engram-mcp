package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/search"
)

// Handler dispatches MCP protocol requests.
type Handler struct {
	service *Service
}

// NewHandler creates a new MCP protocol handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// GetTools returns the specifications for all 8 Engram MCP tools.
func (h *Handler) GetTools() []Tool {
	return []Tool{
		{
			Name:        "engram_search",
			Description: "Hybrid semantic vector and full-text search across notes and ingested papers with automatic graveyard safety warnings.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"query": {
						Type:        "string",
						Description: "The search query (automatically prefixed with search_query: for vector embedding).",
					},
					"paths": {
						Type:        "array",
						Description: "Optional list of path prefixes to filter search (e.g. ['/atlas', '/episodes']).",
						Items:       &PropertyDef{Type: "string"},
					},
					"tags": {
						Type:        "array",
						Description: "Optional frontmatter tags filter.",
						Items:       &PropertyDef{Type: "string"},
					},
					"date_from": {
						Type:        "string",
						Description: "Optional ISO-8601 start date (e.g. '2026-01-01').",
					},
					"date_to": {
						Type:        "string",
						Description: "Optional ISO-8601 end date (e.g. '2026-12-31').",
					},
					"top_k": {
						Type:        "integer",
						Description: "Number of top parent sections to return (default: 5).",
						Default:     5,
					},
					"include_boosted_paths": {
						Type:        "boolean",
						Description: "Whether to evaluate and include /graveyard/ safety warnings (default: true).",
						Default:     true,
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "engram_ls",
			Description: "List workspace files and directories with indexing status, sizes, and frontmatter metadata.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {
						Type:        "string",
						Description: "Directory path relative to workspace root (default: '/').",
						Default:     "/",
					},
					"recursive": {
						Type:        "boolean",
						Description: "Whether to list recursively.",
						Default:     false,
					},
					"pattern": {
						Type:        "string",
						Description: "Optional glob pattern (e.g. '*.md').",
					},
				},
			},
		},
		{
			Name:        "engram_read",
			Description: "Read complete notes or specific sections/line ranges from the workspace ground truth.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {
						Type:        "string",
						Description: "Relative file path (e.g. '/atlas/model_specs.md').",
					},
					"section_or_line_range": {
						Type:        "string",
						Description: "Optional line range (e.g. 'L10-L120') or section heading (e.g. '## Methodology').",
					},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "engram_write",
			Description: "Atomically write a Markdown note to disk and immediately queue parent-child vector indexing.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {
						Type:        "string",
						Description: "Relative file path (e.g. '/episodes/2026-10-16_tuning.md').",
					},
					"content": {
						Type:        "string",
						Description: "Markdown content with optional YAML frontmatter.",
					},
					"overwrite": {
						Type:        "boolean",
						Description: "Whether to overwrite existing files (default: true).",
						Default:     true,
					},
				},
				Required: []string{"path", "content"},
			},
		},
		{
			Name:        "engram_patch",
			Description: "Modify existing notes or append chronological logs without rewriting entire files.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {
						Type:        "string",
						Description: "Relative file path.",
					},
					"operation": {
						Type:        "string",
						Description: "The patch operation to perform.",
						Enum:        []string{"append", "prepend", "replace_section", "replace_text"},
					},
					"target": {
						Type:        "string",
						Description: "Header title (for replace_section) or substring (for replace_text).",
					},
					"content": {
						Type:        "string",
						Description: "The replacement or appended content.",
					},
				},
				Required: []string{"path", "operation", "content"},
			},
		},
		{
			Name:        "engram_mv_rm",
			Description: "Move/rename files (updating DB paths without re-embedding) or safely delete files to .trash/.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"action": {
						Type:        "string",
						Description: "Action to perform ('move' or 'delete').",
						Enum:        []string{"move", "delete"},
					},
					"source_path": {
						Type:        "string",
						Description: "Original file path.",
					},
					"dest_path": {
						Type:        "string",
						Description: "Target file path (required for move).",
					},
				},
				Required: []string{"action", "source_path"},
			},
		},
		{
			Name:        "engram_ingest",
			Description: "Extract text/tables from binary source files (PDF, DOCX, CSV) directly into pgvector chunks without cluttering Markdown folders.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {
						Type:        "string",
						Description: "File path inside /sources/ or workspace.",
					},
					"generate_summary_md": {
						Type:        "boolean",
						Description: "Whether to generate a companion overview note in /atlas/ (default: true).",
						Default:     true,
					},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "engram_bootstrap",
			Description: "Generate or seed canonical workspace instructions into AGENTS.md, GEMINI.md, or CLAUDE.md.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"target_type": {
						Type:        "string",
						Description: "Target rule file format ('AGENTS.md', 'GEMINI.md', 'CLAUDE.md').",
						Enum:        []string{"AGENTS.md", "GEMINI.md", "CLAUDE.md"},
						Default:     "AGENTS.md",
					},
					"custom_rules": {
						Type:        "string",
						Description: "Optional additional rules to append to the instructions.",
					},
				},
			},
		},
	}
}

// HandleCall processes an incoming tools/call request.
func (h *Handler) HandleCall(ctx context.Context, workspaceID string, req ToolCallParams) (*ToolCallResult, error) {
	switch req.Name {
	case "engram_search":
		return h.handleSearch(ctx, workspaceID, req.Arguments)
	case "engram_ls":
		return h.handleList(ctx, workspaceID, req.Arguments)
	case "engram_read":
		return h.handleRead(ctx, workspaceID, req.Arguments)
	case "engram_write":
		return h.handleWrite(ctx, workspaceID, req.Arguments)
	case "engram_patch":
		return h.handlePatch(ctx, workspaceID, req.Arguments)
	case "engram_mv_rm":
		return h.handleMoveOrDelete(ctx, workspaceID, req.Arguments)
	case "engram_ingest":
		return h.handleIngest(ctx, workspaceID, req.Arguments)
	case "engram_bootstrap":
		return h.handleBootstrap(ctx, workspaceID, req.Arguments)
	default:
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{
				{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", req.Name)},
			},
		}, nil
	}
}

func (h *Handler) handleSearch(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	query, _ := args["query"].(string)
	topK := 5
	if tk, ok := args["top_k"].(float64); ok && tk > 0 {
		topK = int(tk)
	}

	includeBoosted := true
	if ib, ok := args["include_boosted_paths"].(bool); ok {
		includeBoosted = ib
	}

	var paths []string
	if rawPaths, ok := args["paths"].([]interface{}); ok {
		for _, p := range rawPaths {
			if s, ok := p.(string); ok {
				paths = append(paths, s)
			}
		}
	}

	var tags []string
	if rawTags, ok := args["tags"].([]interface{}); ok {
		for _, t := range rawTags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	var dateFrom *time.Time
	if df, ok := args["date_from"].(string); ok && df != "" {
		if t, err := time.Parse("2006-01-02", df); err == nil {
			dateFrom = &t
		}
	}

	var dateTo *time.Time
	if dt, ok := args["date_to"].(string); ok && dt != "" {
		if t, err := time.Parse("2006-01-02", dt); err == nil {
			dateTo = &t
		}
	}

	res, err := h.service.Search(ctx, wsID, search.SearchParams{
		Query:               query,
		Paths:               paths,
		Tags:                tags,
		DateFrom:            dateFrom,
		DateTo:              dateTo,
		TopK:                topK,
		IncludeBoostedPaths: includeBoosted,
	})
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Search failed: %v", err)}},
		}, nil
	}

	// Format response as clean JSON text
	outBytes, _ := json.MarshalIndent(res, "", "  ")
	return &ToolCallResult{
		Content: []ToolCallContent{
			{Type: "text", Text: string(outBytes)},
		},
	}, nil
}

func (h *Handler) handleList(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	path, _ := args["path"].(string)
	recursive, _ := args["recursive"].(bool)
	pattern, _ := args["pattern"].(string)

	files, err := h.service.List(ctx, wsID, path, recursive, pattern)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("List failed: %v", err)}},
		}, nil
	}

	outBytes, _ := json.MarshalIndent(files, "", "  ")
	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: string(outBytes)}},
	}, nil
}

func (h *Handler) handleRead(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	path, _ := args["path"].(string)
	rangeOrSec, _ := args["section_or_line_range"].(string)

	content, err := h.service.Read(ctx, wsID, path, rangeOrSec)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Read failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: content}},
	}, nil
}

func (h *Handler) handleWrite(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	overwrite := true
	if ow, ok := args["overwrite"].(bool); ok {
		overwrite = ow
	}

	msg, err := h.service.Write(ctx, wsID, path, content, overwrite)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Write failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: msg}},
	}, nil
}

func (h *Handler) handlePatch(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	path, _ := args["path"].(string)
	op, _ := args["operation"].(string)
	target, _ := args["target"].(string)
	content, _ := args["content"].(string)

	msg, err := h.service.Patch(ctx, wsID, path, op, target, content)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Patch failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: msg}},
	}, nil
}

func (h *Handler) handleMoveOrDelete(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	action, _ := args["action"].(string)
	srcPath, _ := args["source_path"].(string)
	destPath, _ := args["dest_path"].(string)

	msg, err := h.service.MoveOrDelete(ctx, wsID, action, srcPath, destPath)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Move/Delete failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: msg}},
	}, nil
}

func (h *Handler) handleIngest(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	path, _ := args["path"].(string)
	generateSummary := true
	if gs, ok := args["generate_summary_md"].(bool); ok {
		generateSummary = gs
	}

	msg, err := h.service.Ingest(ctx, wsID, path, generateSummary)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Ingest failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: msg}},
	}, nil
}

func (h *Handler) handleBootstrap(ctx context.Context, wsID string, args map[string]interface{}) (*ToolCallResult, error) {
	targetType, _ := args["target_type"].(string)
	customRules, _ := args["custom_rules"].(string)

	rules, err := h.service.Bootstrap(ctx, wsID, targetType, customRules)
	if err != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Bootstrap failed: %v", err)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ToolCallContent{{Type: "text", Text: rules}},
	}, nil
}
