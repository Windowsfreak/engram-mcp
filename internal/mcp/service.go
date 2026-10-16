package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/chunker"
	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/db"
	"github.com/windowsfreak/engram-mcp/internal/embedder"
	"github.com/windowsfreak/engram-mcp/internal/search"
	"github.com/windowsfreak/engram-mcp/internal/vfs"
)

// Service provides the high-level business logic for all 8 Engram MCP tools.
type Service struct {
	cfg          *config.Config
	database     *db.DB
	searchEngine *search.Engine
	chunker      *chunker.Chunker
	workerPool   *embedder.WorkerPool
	vfsMap       map[string]vfs.VFS // workspaceID -> VFS
}

// NewService creates and initializes an Engram Service.
func NewService(
	cfg *config.Config,
	database *db.DB,
	searchEngine *search.Engine,
	chk *chunker.Chunker,
	workerPool *embedder.WorkerPool,
) *Service {
	if chk == nil {
		chk = chunker.NewChunker(cfg.Embedder.Provider)
	}
	return &Service{
		cfg:          cfg,
		database:     database,
		searchEngine: searchEngine,
		chunker:      chk,
		workerPool:   workerPool,
		vfsMap:       make(map[string]vfs.VFS),
	}
}

// GetVFS retrieves or creates a VFS instance for a given workspaceID.
func (s *Service) GetVFS(workspaceID string) (vfs.VFS, error) {
	if workspaceID == "" {
		workspaceID = s.cfg.Storage.DefaultWorkspaceID
	}

	if v, ok := s.vfsMap[workspaceID]; ok {
		return v, nil
	}

	dir, err := s.cfg.WorkspacePath(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed resolving workspace directory: %w", err)
	}

	diskVFS, err := vfs.NewDiskVFS(workspaceID, dir)
	if err != nil {
		return nil, fmt.Errorf("failed initializing VFS: %w", err)
	}

	// Ensure default directory taxonomy exists on disk
	defaultDirs := []string{"/00_Inbox", "/atlas", "/episodes", "/graveyard", "/persona", "/sources"}
	for _, d := range defaultDirs {
		p, err := diskVFS.ToDiskPath(d)
		if err == nil {
			_ = os.MkdirAll(p, 0755)
		}
	}

	s.vfsMap[workspaceID] = diskVFS
	return diskVFS, nil
}

// Search implements engram_search.
func (s *Service) Search(ctx context.Context, workspaceID string, params search.SearchParams) (*search.SearchResponse, error) {
	params.WorkspaceID = workspaceID
	return s.searchEngine.Search(ctx, params)
}

// FileEntry represents a file in engram_ls output.
type FileEntry struct {
	Path        string                 `json:"path"`
	Name        string                 `json:"name"`
	IsDir       bool                   `json:"is_dir"`
	SizeBytes   int64                  `json:"size_bytes"`
	ModifiedAt  string                 `json:"modified_at"`
	Status      string                 `json:"status"` // indexed, pending, error, untracked
	Title       string                 `json:"title,omitempty"`
	Frontmatter map[string]interface{} `json:"frontmatter,omitempty"`
}

// List implements engram_ls.
func (s *Service) List(ctx context.Context, workspaceID, relPath string, recursive bool, pattern string) ([]FileEntry, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return nil, err
	}

	if relPath == "" {
		relPath = "/"
	}

	diskFiles, err := v.ListFiles(relPath, recursive)
	if err != nil {
		return nil, err
	}

	// Fetch DB document index status
	docMap := make(map[string]*db.Document)
	if s.database != nil {
		dbDocs, _ := s.database.ListDocuments(ctx, workspaceID, relPath)
		for _, d := range dbDocs {
			docMap[d.Path] = d
		}
	}

	var results []FileEntry
	for _, f := range diskFiles {
		// Pattern matching (glob) if provided
		if pattern != "" {
			matched, _ := filepath.Match(pattern, f.Name)
			if !matched {
				continue
			}
		}

		entry := FileEntry{
			Path:       f.Path,
			Name:       f.Name,
			IsDir:      f.IsDir,
			SizeBytes:  f.SizeBytes,
			ModifiedAt: f.ModifiedAt.Format(time.RFC3339),
			Status:     "untracked",
		}

		if doc, ok := docMap[f.Path]; ok {
			entry.Status = doc.Status
			entry.Title = doc.Title
			entry.Frontmatter = doc.Frontmatter
		}

		results = append(results, entry)
	}

	return results, nil
}

// Read implements engram_read.
func (s *Service) Read(ctx context.Context, workspaceID, relPath, sectionOrRange string) (string, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return "", err
	}

	data, err := v.ReadFile(relPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	if sectionOrRange == "" {
		return content, nil
	}

	sectionOrRange = strings.TrimSpace(sectionOrRange)

	// Case 1: Line range (e.g., L10-L50)
	if strings.HasPrefix(sectionOrRange, "L") && strings.Contains(sectionOrRange, "-") {
		return extractLineRange(content, sectionOrRange)
	}

	// Case 2: Markdown Section Heading (e.g., "## Methodology" or "Methodology")
	cleanHeading := strings.TrimLeft(sectionOrRange, "# \t")
	sections := chunker.ParseMarkdownSections(content)
	for _, sec := range sections {
		if strings.EqualFold(strings.TrimSpace(sec.Heading), cleanHeading) {
			return sec.Content, nil
		}
	}

	// If no section match, return full content
	return content, nil
}

func extractLineRange(content, lineRange string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(lineRange, "L"), "-L")
	if len(parts) != 2 {
		parts = strings.Split(strings.TrimPrefix(lineRange, "L"), "-")
	}
	if len(parts) != 2 {
		return content, nil
	}

	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || start < 1 || end < start {
		return content, nil
	}

	lines := strings.Split(content, "\n")
	if start > len(lines) {
		return "", nil
	}
	if end > len(lines) {
		end = len(lines)
	}

	return strings.Join(lines[start-1:end], "\n"), nil
}

// Write implements engram_write.
func (s *Service) Write(ctx context.Context, workspaceID, relPath, content string, overwrite bool) (string, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return "", err
	}

	exists, _ := v.FileExists(relPath)
	if exists && !overwrite {
		return "", fmt.Errorf("file already exists at %s and overwrite is false", relPath)
	}

	// Write atomically to disk
	data := []byte(content)
	if err := v.WriteFileAtomic(relPath, data); err != nil {
		return "", fmt.Errorf("failed writing file to disk: %w", err)
	}

	// Compute hash and stats
	sha, size, err := v.ComputeHash(relPath)
	if err != nil {
		return "", err
	}

	ext := strings.TrimPrefix(filepath.Ext(relPath), ".")
	doc := &db.Document{
		WorkspaceID: workspaceID,
		Path:        relPath,
		SHA256:      sha,
		SizeBytes:   size,
		FileType:    ext,
		ModifiedAt:  time.Now(),
		Status:      "pending",
	}

	// Process parent-child chunks
	chunkResult, err := s.chunker.ProcessDocument(doc, data)
	if err != nil {
		return "", fmt.Errorf("failed chunking document: %w", err)
	}

	// Upsert doc & replace sections, chunks, embedding jobs in DB
	if s.database != nil {
		if err := s.database.UpsertDocument(ctx, chunkResult.Document); err != nil {
			return "", fmt.Errorf("failed upserting document: %w", err)
		}

		if err := s.database.ReplaceDocumentSectionsAndChunks(
			ctx,
			workspaceID,
			relPath,
			chunkResult.Sections,
			chunkResult.Chunks,
			chunkResult.Jobs,
		); err != nil {
			return "", fmt.Errorf("failed saving chunks: %w", err)
		}
	}

	// Trigger worker pool to immediately embed queued chunks
	if s.workerPool != nil {
		s.workerPool.Trigger()
	}

	return fmt.Sprintf("Successfully wrote %s (%d bytes, %d parent sections, %d child chunks queued for embedding)",
		relPath, size, len(chunkResult.Sections), len(chunkResult.Chunks)), nil
}

// Patch implements engram_patch.
func (s *Service) Patch(ctx context.Context, workspaceID, relPath, operation, target, patchContent string) (string, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return "", err
	}

	existingBytes, err := v.ReadFile(relPath)
	if err != nil {
		return "", fmt.Errorf("failed reading target file: %w", err)
	}

	existingText := string(existingBytes)
	var newText string

	switch operation {
	case "append":
		if strings.HasSuffix(existingText, "\n") {
			newText = existingText + patchContent
		} else {
			newText = existingText + "\n\n" + patchContent
		}
	case "prepend":
		newText = patchContent + "\n\n" + existingText
	case "replace_text":
		if target == "" {
			return "", errors.New("target text must be specified for replace_text")
		}
		if !strings.Contains(existingText, target) {
			return "", fmt.Errorf("target string not found in document %s", relPath)
		}
		newText = strings.Replace(existingText, target, patchContent, 1)
	case "replace_section":
		cleanTarget := strings.TrimLeft(target, "# \t")
		sections := chunker.ParseMarkdownSections(existingText)
		found := false
		var sb strings.Builder
		for _, sec := range sections {
			if strings.EqualFold(strings.TrimSpace(sec.Heading), cleanTarget) {
				found = true
				sb.WriteString(patchContent)
				sb.WriteString("\n\n")
			} else {
				if sec.Heading != "" && sec.HeadingLevel > 0 {
					sb.WriteString(strings.Repeat("#", sec.HeadingLevel) + " " + sec.Heading + "\n\n")
				}
				sb.WriteString(sec.Content)
				sb.WriteString("\n\n")
			}
		}
		if !found {
			return "", fmt.Errorf("section %q not found in document %s", target, relPath)
		}
		newText = strings.TrimRight(sb.String(), "\n") + "\n"
	default:
		return "", fmt.Errorf("unsupported patch operation: %s", operation)
	}

	// Atomically write updated document and re-index
	return s.Write(ctx, workspaceID, relPath, newText, true)
}

// MoveOrDelete implements engram_mv_rm.
func (s *Service) MoveOrDelete(ctx context.Context, workspaceID, action, srcPath, destPath string) (string, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return "", err
	}

	switch action {
	case "move":
		if destPath == "" {
			return "", errors.New("dest_path must be specified for move operation")
		}
		if err := v.MoveFile(srcPath, destPath); err != nil {
			return "", fmt.Errorf("failed moving file on disk: %w", err)
		}
		if s.database != nil {
			if err := s.database.MoveDocument(ctx, workspaceID, srcPath, destPath); err != nil {
				return "", fmt.Errorf("failed updating database path: %w", err)
			}
		}
		return fmt.Sprintf("Successfully moved %s to %s (all vector embeddings preserved)", srcPath, destPath), nil

	case "delete":
		trashRelPath, err := v.SafeDelete(srcPath)
		if err != nil {
			return "", fmt.Errorf("failed deleting file: %w", err)
		}
		if s.database != nil {
			if err := s.database.DeleteDocument(ctx, workspaceID, srcPath); err != nil {
				return "", fmt.Errorf("failed deleting document from index: %w", err)
			}
		}
		return fmt.Sprintf("Successfully deleted %s (safely preserved in %s and removed from active index)", srcPath, trashRelPath), nil

	default:
		return "", fmt.Errorf("invalid action: %s (must be 'move' or 'delete')", action)
	}
}

// Ingest implements engram_ingest.
func (s *Service) Ingest(ctx context.Context, workspaceID, relPath string, generateSummary bool) (string, error) {
	v, err := s.GetVFS(workspaceID)
	if err != nil {
		return "", err
	}

	data, err := v.ReadFile(relPath)
	if err != nil {
		return "", fmt.Errorf("failed reading source file %s: %w", relPath, err)
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
	sha, size, err := v.ComputeHash(relPath)
	if err != nil {
		return "", err
	}

	doc := &db.Document{
		WorkspaceID: workspaceID,
		Path:        relPath,
		SHA256:      sha,
		SizeBytes:   size,
		FileType:    ext,
		ModifiedAt:  time.Now(),
		Status:      "pending",
	}

	chunkResult, err := s.chunker.ProcessDocument(doc, data)
	if err != nil {
		return "", fmt.Errorf("failed chunking source document: %w", err)
	}

	if err := s.database.UpsertDocument(ctx, chunkResult.Document); err != nil {
		return "", err
	}

	if err := s.database.ReplaceDocumentSectionsAndChunks(
		ctx,
		workspaceID,
		relPath,
		chunkResult.Sections,
		chunkResult.Chunks,
		chunkResult.Jobs,
	); err != nil {
		return "", err
	}

	if s.workerPool != nil {
		s.workerPool.Trigger()
	}

	summaryMsg := fmt.Sprintf("Ingested %s (%d parent sections, %d child vectors queued)",
		relPath, len(chunkResult.Sections), len(chunkResult.Chunks))

	// Optionally generate a summary Markdown note in /atlas/
	if generateSummary {
		baseName := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
		summaryPath := fmt.Sprintf("/atlas/%s_overview.md", baseName)
		summaryContent := fmt.Sprintf(`---
title: "Source Overview: %s"
source_path: "%s"
date: %s
tags: ["source", "overview", "%s"]
---

# Source Overview: %s

This document synthesizes the ingested publication located at `+"`%s`"+`.

* Ingested Sections: %d
* Ingested Child Chunks: %d
* File Size: %d bytes
`, baseName, relPath, time.Now().Format("2006-01-02"), ext, baseName, relPath, len(chunkResult.Sections), len(chunkResult.Chunks), size)

		_, _ = s.Write(ctx, workspaceID, summaryPath, summaryContent, true)
		summaryMsg += fmt.Sprintf(" and created overview at %s", summaryPath)
	}

	return summaryMsg, nil
}

// Bootstrap implements engram_bootstrap.
func (s *Service) Bootstrap(ctx context.Context, workspaceID, targetType, customRules string) (string, error) {
	if targetType == "" {
		targetType = "AGENTS.md"
	}

	content := RenderBootstrapContent(workspaceID, targetType, customRules)

	// Persist to /persona/agent_rules.md in the workspace
	_, _ = s.Write(ctx, workspaceID, "/persona/agent_rules.md", content, true)

	return content, nil
}
