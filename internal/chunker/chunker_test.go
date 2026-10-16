package chunker_test

import (
	"strings"
	"testing"

	"github.com/windowsfreak/engram-mcp/internal/chunker"
	"github.com/windowsfreak/engram-mcp/internal/db"
)

func TestFrontmatterAndSectionParsing(t *testing.T) {
	sample := `---
title: "Multi-Agent Architectures"
tags: ["agents", "mcp", "memory"]
date: 2026-10-15
---

# Multi-Agent Architectures

Overview of coordination strategies between autonomous agents.

## Section 1: Memory Primitives

Agents require durable episodic and procedural memory stores.
A filesystem ground truth avoids proprietary database locking.

## Section 2: Negative Knowledge (Graveyard)

Recording failed hypothesis paths prevents infinite search loops.
`

	parsed := chunker.ParseFrontmatter(sample)
	if parsed.Title != "Multi-Agent Architectures" {
		t.Errorf("expected title 'Multi-Agent Architectures', got: %s", parsed.Title)
	}

	tags, ok := parsed.Frontmatter["tags"].([]interface{})
	if !ok || len(tags) != 3 {
		t.Errorf("expected 3 tags in frontmatter, got: %+v", parsed.Frontmatter)
	}

	sections := chunker.ParseMarkdownSections(parsed.Body)
	if len(sections) < 3 {
		t.Errorf("expected at least 3 sections, got %d", len(sections))
	}
	if sections[0].Heading != "Multi-Agent Architectures" {
		t.Errorf("expected section 0 heading 'Multi-Agent Architectures', got: %s", sections[0].Heading)
	}
}

func TestCSVConversion(t *testing.T) {
	csvData := "experiment_id,metric,value\nexp_01,loss,0.024\nexp_02,loss,0.019\n"
	md, err := chunker.ConvertCSVToMarkdown(csvData)
	if err != nil {
		t.Fatalf("unexpected error converting csv: %v", err)
	}

	if !strings.Contains(md, "| experiment_id | metric | value |") {
		t.Errorf("expected header row, got:\n%s", md)
	}
	if !strings.Contains(md, "| exp_01 | loss | 0.024 |") {
		t.Errorf("expected data row, got:\n%s", md)
	}
}

func TestChunkerMandatoryPrefixAndTokenLimits(t *testing.T) {
	c := chunker.NewChunker("ollama")

	doc := &db.Document{
		WorkspaceID: "default",
		Path:        "/atlas/algorithms.md",
		FileType:    "md",
	}

	content := `---
title: "Large Note"
---

# High-Performance Retrieval

Here is a paragraph that will be chunked into child vectors.
Each child vector must strictly adhere to the nomic-embed-text-v2-moe context limit (under 512 tokens).
Our chunker targets 250 to 400 tokens with a 50-token overlap.

## Second Section

More content explaining reciprocal rank fusion and vector indexing.
`

	result, err := c.ProcessDocument(doc, []byte(content))
	if err != nil {
		t.Fatalf("failed processing document: %v", err)
	}

	if len(result.Sections) == 0 {
		t.Fatal("expected at least 1 section")
	}
	if len(result.Chunks) == 0 {
		t.Fatal("expected at least 1 child chunk")
	}
	if len(result.Jobs) != len(result.Chunks) {
		t.Errorf("expected %d jobs, got %d", len(result.Chunks), len(result.Jobs))
	}

	// Verify mandatory "search_document: " prefix on all embedding jobs
	for i, job := range result.Jobs {
		if !strings.HasPrefix(job.InputText, "search_document: ") {
			t.Errorf("job %d missing mandatory 'search_document: ' prefix: %s", i, job.InputText)
		}
	}

	// Verify child chunk token counts are well below the 512 ceiling
	for i, chunk := range result.Chunks {
		if chunk.TokenCount > 500 {
			t.Errorf("chunk %d exceeded 500 tokens: %d", i, chunk.TokenCount)
		}
	}
}

func TestChunkerCRLFProcessing(t *testing.T) {
	c := chunker.NewChunker("sidecar")

	doc := &db.Document{
		WorkspaceID: "default",
		Path:        "/atlas/windows_note.md",
		FileType:    "md",
	}

	crlfContent := "---\r\ntitle: \"Windows Note\"\r\ntags: [\"windows\", \"crlf\"]\r\n---\r\n\r\n# Section One\r\n\r\nParagraph with CRLF endings.\r\n\r\n## Section Two\r\n\r\nAnother paragraph with Windows line endings.\r\n"

	result, err := c.ProcessDocument(doc, []byte(crlfContent))
	if err != nil {
		t.Fatalf("failed processing CRLF document: %v", err)
	}

	if result.Document.Title != "Windows Note" {
		t.Errorf("expected title 'Windows Note', got: %s", result.Document.Title)
	}

	if len(result.Sections) < 2 {
		t.Errorf("expected at least 2 sections, got: %d", len(result.Sections))
	}
}

