package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/windowsfreak/engram-mcp/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg == nil {
		t.Fatal("expected default config, got nil")
	}
	if cfg.Embedder.Ollama.Model != "nomic-ai/nomic-embed-text-v2-moe" {
		t.Errorf("unexpected default model: %s", cfg.Embedder.Ollama.Model)
	}
	if cfg.Embedder.Ollama.Dimensions != 768 {
		t.Errorf("unexpected default dimensions: %d", cfg.Embedder.Ollama.Dimensions)
	}
	if cfg.Embedder.Sidecar.Model != "nomic-ai/nomic-embed-text-v2-moe" {
		t.Errorf("unexpected sidecar model: %s", cfg.Embedder.Sidecar.Model)
	}
	if cfg.Embedder.Sidecar.Dimensions != 768 {
		t.Errorf("unexpected sidecar dimensions: %d", cfg.Embedder.Sidecar.Dimensions)
	}
	if cfg.Search.RRFConstantK != 60 {
		t.Errorf("unexpected default RRF constant: %d", cfg.Search.RRFConstantK)
	}
}

func TestLoadWithYAML(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	yamlContent := `
database:
  url: "postgres://custom:pass@db:5432/engram_test"
storage:
  base_dir: "/tmp/engram_test"
  default_workspace_id: "research"
server:
  http_addr: ":9090"
  bearer_tokens:
    "secret-token-123": "research"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Database.URL != "postgres://custom:pass@db:5432/engram_test" {
		t.Errorf("unexpected db url: %s", cfg.Database.URL)
	}
	if cfg.Storage.DefaultWorkspaceID != "research" {
		t.Errorf("unexpected default workspace: %s", cfg.Storage.DefaultWorkspaceID)
	}
	if cfg.Server.HTTPAddr != ":9090" {
		t.Errorf("unexpected http addr: %s", cfg.Server.HTTPAddr)
	}

	ws := cfg.ResolveWorkspaceForToken("Bearer secret-token-123")
	if ws != "research" {
		t.Errorf("expected workspace 'research' for token, got %s", ws)
	}
}
