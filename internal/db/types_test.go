package db_test

import (
	"testing"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/db"
)

func TestDocumentFrontmatterBytes(t *testing.T) {
	doc := &db.Document{
		WorkspaceID: "default",
		Path:        "/atlas/test.md",
		Title:       "Test Document",
		SHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		FileType:    "md",
		ModifiedAt:  time.Now(),
		Frontmatter: map[string]interface{}{
			"tags":   []string{"ml", "research"},
			"author": "Antigravity",
		},
	}

	bytes, err := doc.FrontmatterBytes()
	if err != nil {
		t.Fatalf("unexpected error serializing frontmatter: %v", err)
	}

	if len(bytes) == 0 || string(bytes) == "{}" {
		t.Errorf("expected non-empty JSON frontmatter, got: %s", string(bytes))
	}
}

func TestNilFrontmatter(t *testing.T) {
	doc := &db.Document{
		WorkspaceID: "default",
		Path:        "/atlas/empty.md",
		Frontmatter: nil,
	}

	bytes, err := doc.FrontmatterBytes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(bytes) != "{}" {
		t.Errorf("expected '{}', got: %s", string(bytes))
	}
}
