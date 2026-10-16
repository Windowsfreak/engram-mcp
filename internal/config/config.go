package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Database DatabaseConfig `yaml:"database"`
	Storage  StorageConfig  `yaml:"storage"`
	Embedder EmbedderConfig `yaml:"embedder"`
	Search   SearchConfig   `yaml:"search"`
	Server   ServerConfig   `yaml:"server"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL            string `yaml:"url"`
	MaxConnections int32  `yaml:"max_connections"`
	MinConnections int32  `yaml:"min_connections"`
}

// StorageConfig holds workspace and filesystem settings.
type StorageConfig struct {
	BaseDir            string            `yaml:"base_dir"`
	DefaultWorkspaceID string            `yaml:"default_workspace_id"`
	Workspaces         map[string]string `yaml:"workspaces"` // workspace_id -> absolute or relative path
}

// EmbedderConfig holds settings for vector embedding generation.
type EmbedderConfig struct {
	Provider string        `yaml:"provider"` // "sidecar", "ollama", or "openai"
	Sidecar  SidecarConfig `yaml:"sidecar"`
	Ollama   OllamaConfig  `yaml:"ollama"`
	OpenAI   OpenAIConfig  `yaml:"openai"`
}

// SidecarConfig configures the dedicated Uvicorn/FastAPI embedding sidecar.
type SidecarConfig struct {
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
	BatchSize  int    `yaml:"batch_size"`
	TimeoutSec int    `yaml:"timeout_sec"`
}

// OllamaConfig configures the local/LAN Ollama service.
type OllamaConfig struct {
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
	TimeoutSec int    `yaml:"timeout_sec"`
}

// OpenAIConfig configures OpenAI or compatible (freellmapi) endpoints.
type OpenAIConfig struct {
	BaseURL        string `yaml:"base_url"`
	APIKey         string `yaml:"api_key"`
	Model          string `yaml:"model"`
	Dimensions     int    `yaml:"dimensions"`
	BatchSize      int    `yaml:"batch_size"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	TimeoutSec     int    `yaml:"timeout_sec"`
}

// SearchConfig configures the hybrid search and RRF engine.
type SearchConfig struct {
	DefaultTopK       int      `yaml:"default_top_k"`
	RRFConstantK      int      `yaml:"rrf_constant_k"`
	GraveyardPrefixes []string `yaml:"graveyard_prefixes"`
}

// ServerConfig configures MCP stdio and HTTP endpoints.
type ServerConfig struct {
	HTTPAddr     string            `yaml:"http_addr"`
	BearerTokens map[string]string `yaml:"bearer_tokens"` // token -> workspace_id
}

// DefaultConfig returns a production-ready default configuration.
func DefaultConfig() *Config {
	return &Config{
		Database: DatabaseConfig{
			URL:            "postgres://postgres:postgres@localhost:5432/engram?sslmode=disable",
			MaxConnections: 10,
			MinConnections: 2,
		},
		Storage: StorageConfig{
			BaseDir:            "./data/workspaces",
			DefaultWorkspaceID: "default",
			Workspaces: map[string]string{
				"default": "./data/workspaces/default",
			},
		},
		Embedder: EmbedderConfig{
			Provider: "sidecar",
			Sidecar: SidecarConfig{
				BaseURL:    "http://localhost:8001",
				Model:      "nomic-ai/nomic-embed-text-v2-moe",
				Dimensions: 768,
				BatchSize:  64,
				TimeoutSec: 30,
			},
			Ollama: OllamaConfig{
				BaseURL:    "http://localhost:11434",
				Model:      "nomic-ai/nomic-embed-text-v2-moe",
				Dimensions: 768,
				TimeoutSec: 60,
			},
			OpenAI: OpenAIConfig{
				BaseURL:        "https://api.openai.com/v1",
				APIKey:         "",
				Model:          "text-embedding-3-small",
				Dimensions:     768,
				BatchSize:      32,
				MaxConcurrency: 4,
				TimeoutSec:     60,
			},
		},
		Search: SearchConfig{
			DefaultTopK:       5,
			RRFConstantK:      60,
			GraveyardPrefixes: []string{"/graveyard/", "/graveyard"},
		},
		Server: ServerConfig{
			HTTPAddr:     ":8080",
			BearerTokens: make(map[string]string),
		},
	}
}

// Load loads configuration from a YAML file, falling back to environment variables.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
		}
	}

	// Environment variable overrides
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		cfg.Database.URL = dbURL
	}
	if baseDir := os.Getenv("ENGRAM_BASE_DIR"); baseDir != "" {
		cfg.Storage.BaseDir = baseDir
	}
	if embedProvider := os.Getenv("ENGRAM_EMBED_PROVIDER"); embedProvider != "" {
		cfg.Embedder.Provider = embedProvider
	}
	if sidecarURL := os.Getenv("SIDECAR_BASE_URL"); sidecarURL != "" {
		cfg.Embedder.Sidecar.BaseURL = sidecarURL
	}
	if ollamaURL := os.Getenv("OLLAMA_BASE_URL"); ollamaURL != "" {
		cfg.Embedder.Ollama.BaseURL = ollamaURL
	}
	if openAIKey := os.Getenv("OPENAI_API_KEY"); openAIKey != "" {
		cfg.Embedder.OpenAI.APIKey = openAIKey
	}
	if openAIBase := os.Getenv("OPENAI_BASE_URL"); openAIBase != "" {
		cfg.Embedder.OpenAI.BaseURL = openAIBase
	}
	if httpAddr := os.Getenv("ENGRAM_HTTP_ADDR"); httpAddr != "" {
		cfg.Server.HTTPAddr = httpAddr
	}

	// Normalize workspace paths
	for wsID, dir := range cfg.Storage.Workspaces {
		absPath, err := filepath.Abs(dir)
		if err == nil {
			cfg.Storage.Workspaces[wsID] = absPath
		}
	}

	return cfg, nil
}

// WorkspacePath returns the absolute path on disk for a given workspace_id.
func (c *Config) WorkspacePath(workspaceID string) (string, error) {
	if workspaceID == "" {
		workspaceID = c.Storage.DefaultWorkspaceID
	}
	if p, ok := c.Storage.Workspaces[workspaceID]; ok {
		return p, nil
	}

	// Default to base_dir/workspaceID
	cleanBase, err := filepath.Abs(c.Storage.BaseDir)
	if err != nil {
		return "", fmt.Errorf("invalid base storage dir: %w", err)
	}
	target := filepath.Join(cleanBase, workspaceID)
	return target, nil
}

// ResolveWorkspaceForToken returns the workspace_id mapped to a Bearer token.
func (c *Config) ResolveWorkspaceForToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	if wsID, ok := c.Server.BearerTokens[token]; ok && wsID != "" {
		return wsID
	}
	return c.Storage.DefaultWorkspaceID
}
