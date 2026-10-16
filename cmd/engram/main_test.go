package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windowsfreak/engram-mcp/internal/chunker"
	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/mcp"
	"github.com/windowsfreak/engram-mcp/internal/search"
	"github.com/windowsfreak/engram-mcp/internal/vfs"
)

type mockEmbedder struct{}

func (m *mockEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	return make([]float32, 768), nil
}

func (m *mockEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 768)
	}
	return vecs, nil
}

func (m *mockEmbedder) Dimensions() int {
	return 768
}

func (m *mockEmbedder) Model() string {
	return "nomic-ai/nomic-embed-text-v2-moe"
}

func TestEndToEndLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "default")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("failed creating workspace dir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.BaseDir = tempDir
	cfg.Storage.DefaultWorkspaceID = "default"
	cfg.Storage.Workspaces = map[string]string{
		"default": workspaceDir,
	}

	mockEmb := &mockEmbedder{}
	searchEng := search.NewEngine(nil, mockEmb, &cfg.Search)
	chk := chunker.NewChunker("ollama")

	// Service without DB to test complete VFS, chunker, patch, and bootstrap pipeline
	svc := mcp.NewService(cfg, nil, searchEng, chk, nil)
	handler := mcp.NewHandler(svc)
	ctx := context.Background()

	// 1. Bootstrap Workspace
	bootRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_bootstrap",
		Arguments: map[string]interface{}{
			"target_type":  "GEMINI.md",
			"custom_rules": "Always verify Kalman Filter variance bounds.",
		},
	})
	if err != nil || bootRes.IsError {
		t.Fatalf("engram_bootstrap failed: %v", err)
	}
	if !strings.Contains(bootRes.Content[0].Text, "Kalman Filter variance bounds") {
		t.Errorf("bootstrap output missing custom rules: %s", bootRes.Content[0].Text)
	}

	// 2. Write note to /atlas/
	noteContent := `---
title: "Kalman Filter Derivation"
tags: ["control-theory", "state-estimation"]
date: 2026-10-16
---

# Kalman Filter Derivation

The linear quadratic estimation algorithm computes optimal state estimates.

## Prediction Step

The state estimate is propagated using the transition matrix $F_k$.
State covariance is updated by $P_k = F_k P_{k-1} F_k^T + Q_k$.
`
	writeRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_write",
		Arguments: map[string]interface{}{
			"path":      "/atlas/kalman.md",
			"content":   noteContent,
			"overwrite": true,
		},
	})
	if err != nil || writeRes.IsError {
		t.Fatalf("engram_write failed: %v", err)
	}

	// 3. List workspace files via engram_ls
	lsRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_ls",
		Arguments: map[string]interface{}{
			"path":      "/",
			"recursive": true,
		},
	})
	if err != nil || lsRes.IsError {
		t.Fatalf("engram_ls failed: %v", err)
	}
	if !strings.Contains(lsRes.Content[0].Text, "kalman.md") {
		t.Errorf("engram_ls output missing written file: %s", lsRes.Content[0].Text)
	}

	// 4. Read section via engram_read
	readRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_read",
		Arguments: map[string]interface{}{
			"path":                  "/atlas/kalman.md",
			"section_or_line_range": "## Prediction Step",
		},
	})
	if err != nil || readRes.IsError {
		t.Fatalf("engram_read failed: %v", err)
	}
	if !strings.Contains(readRes.Content[0].Text, "transition matrix") {
		t.Errorf("read section missing expected content: %s", readRes.Content[0].Text)
	}

	// 5. Append episodic log via engram_patch
	patchRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_patch",
		Arguments: map[string]interface{}{
			"path":      "/atlas/kalman.md",
			"operation": "append",
			"content":   "## Update Step\nMeasurement update calculates the Kalman gain $K_k$.",
		},
	})
	if err != nil || patchRes.IsError {
		t.Fatalf("engram_patch failed: %v", err)
	}

	// Verify appended content
	readFull, _ := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_read",
		Arguments: map[string]interface{}{
			"path": "/atlas/kalman.md",
		},
	})
	if !strings.Contains(readFull.Content[0].Text, "Kalman gain") {
		t.Errorf("expected patched text to contain Kalman gain: %s", readFull.Content[0].Text)
	}

	// 6. Test Safe Delete to .trash/
	delRes, err := handler.HandleCall(ctx, "default", mcp.ToolCallParams{
		Name: "engram_mv_rm",
		Arguments: map[string]interface{}{
			"action":      "delete",
			"source_path": "/atlas/kalman.md",
		},
	})
	if err != nil || delRes.IsError {
		t.Fatalf("engram_mv_rm delete failed: %v", err)
	}
	if !strings.Contains(delRes.Content[0].Text, ".trash") {
		t.Errorf("expected delete result to reference .trash: %s", delRes.Content[0].Text)
	}
}

func TestVFSWatcherReconciliation(t *testing.T) {
	tempDir := t.TempDir()
	diskVFS, err := vfs.NewDiskVFS("default", tempDir)
	if err != nil {
		t.Fatalf("failed creating DiskVFS: %v", err)
	}

	// Create test file directly on disk
	testFile := filepath.Join(tempDir, "direct_disk_note.md")
	if err := os.WriteFile(testFile, []byte("# Note from Obsidian\nDirect disk write."), 0644); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	files, err := diskVFS.ListFiles("/", false)
	if err != nil {
		t.Fatalf("failed listing files: %v", err)
	}

	found := false
	for _, f := range files {
		if f.Path == "/direct_disk_note.md" {
			found = true
			if f.SHA256 == "" {
				t.Errorf("expected SHA256 computed for direct disk file")
			}
		}
	}

	if !found {
		t.Errorf("expected /direct_disk_note.md in list, got: %+v", files)
	}
}
