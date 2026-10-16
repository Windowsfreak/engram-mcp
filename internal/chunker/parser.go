package chunker

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParsedDocument contains the parsed frontmatter and body.
type ParsedDocument struct {
	Title       string
	Frontmatter map[string]interface{}
	Body        string
}

// ParseFrontmatter extracts YAML frontmatter delimited by "---" from Markdown.
func ParseFrontmatter(content string) ParsedDocument {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return ParsedDocument{
			Frontmatter: make(map[string]interface{}),
			Body:        content,
		}
	}

	// Find the closing ---
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return ParsedDocument{
			Frontmatter: make(map[string]interface{}),
			Body:        content,
		}
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			closeIdx = i
			break
		}
	}

	if closeIdx == -1 {
		return ParsedDocument{
			Frontmatter: make(map[string]interface{}),
			Body:        content,
		}
	}

	yamlBlock := strings.Join(lines[1:closeIdx], "\n")
	body := strings.Join(lines[closeIdx+1:], "\n")

	fm := make(map[string]interface{})
	_ = yaml.Unmarshal([]byte(yamlBlock), &fm)

	var title string
	if t, ok := fm["title"].(string); ok {
		title = t
	}

	return ParsedDocument{
		Title:       title,
		Frontmatter: fm,
		Body:        body,
	}
}

// RawSection holds intermediate parsed section information.
type RawSection struct {
	Heading      string
	HeadingLevel int
	Content      string
	TokenCount   int
}

// ParseMarkdownSections splits Markdown content into logical parent sections (1,200–2,500 tokens).
func ParseMarkdownSections(markdown string) []RawSection {
	lines := strings.Split(markdown, "\n")
	var sections []RawSection

	var currentHeading string
	var currentLevel int
	var currentLines []string

	flushSection := func() {
		text := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if text == "" {
			return
		}

		tokCount := EstimateTokens(text)
		// If a section is excessively large (> 2,500 tokens), split it into multiple parts at paragraph boundaries
		if tokCount > 2500 {
			subParts := splitLargeText(text, 1800)
			for i, part := range subParts {
				subHeading := currentHeading
				if i > 0 && currentHeading != "" {
					subHeading = fmt.Sprintf("%s (Part %d)", currentHeading, i+1)
				}
				sections = append(sections, RawSection{
					Heading:      subHeading,
					HeadingLevel: currentLevel,
					Content:      part,
					TokenCount:   EstimateTokens(part),
				})
			}
		} else {
			sections = append(sections, RawSection{
				Heading:      currentHeading,
				HeadingLevel: currentLevel,
				Content:      text,
				TokenCount:   tokCount,
			})
		}
		currentLines = nil
	}

	inCodeBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fence to avoid treating # inside code blocks as headers
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			currentLines = append(currentLines, line)
			continue
		}

		if !inCodeBlock && strings.HasPrefix(trimmed, "#") {
			level, headerText := parseHeaderLine(trimmed)
			if level > 0 {
				// Flush accumulated text before starting a new section
				flushSection()
				currentHeading = headerText
				currentLevel = level
				currentLines = append(currentLines, line)
				continue
			}
		}

		currentLines = append(currentLines, line)
	}

	flushSection()

	// If no headers were found and no sections created, treat entire text as one section
	if len(sections) == 0 {
		trimmed := strings.TrimSpace(markdown)
		if trimmed != "" {
			tokCount := EstimateTokens(trimmed)
			if tokCount > 2500 {
				parts := splitLargeText(trimmed, 1800)
				for i, p := range parts {
					sections = append(sections, RawSection{
						Heading:      fmt.Sprintf("Part %d", i+1),
						HeadingLevel: 0,
						Content:      p,
						TokenCount:   EstimateTokens(p),
					})
				}
			} else {
				sections = append(sections, RawSection{
					Heading:      "",
					HeadingLevel: 0,
					Content:      trimmed,
					TokenCount:   tokCount,
				})
			}
		}
	}

	return sections
}

func parseHeaderLine(line string) (int, string) {
	level := 0
	for _, ch := range line {
		if ch == '#' {
			level++
		} else {
			break
		}
	}
	if level > 6 || level == 0 {
		return 0, ""
	}
	rest := strings.TrimSpace(line[level:])
	if rest == "" {
		return 0, ""
	}
	return level, rest
}

// splitLargeText splits text into pieces of roughly targetTokens at paragraph boundaries (\n\n).
func splitLargeText(text string, targetTokens int) []string {
	paragraphs := strings.Split(text, "\n\n")
	var result []string
	var current []string
	currentTokens := 0

	for _, p := range paragraphs {
		pTokens := EstimateTokens(p)
		if currentTokens+pTokens > targetTokens && len(current) > 0 {
			result = append(result, strings.Join(current, "\n\n"))
			current = []string{p}
			currentTokens = pTokens
		} else {
			current = append(current, p)
			currentTokens += pTokens
		}
	}

	if len(current) > 0 {
		result = append(result, strings.Join(current, "\n\n"))
	}

	return result
}

// ConvertCSVToMarkdown parses a CSV string into a readable Markdown table representation.
func ConvertCSVToMarkdown(csvContent string) (string, error) {
	r := csv.NewReader(strings.NewReader(csvContent))
	var sb strings.Builder

	records, err := r.ReadAll()
	if err != nil {
		return "", fmt.Errorf("failed reading csv: %w", err)
	}

	if len(records) == 0 {
		return "", nil
	}

	// Header row
	headers := records[0]
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")

	// Separator row
	var separators []string
	for range headers {
		separators = append(separators, "---")
	}
	sb.WriteString("| " + strings.Join(separators, " | ") + " |\n")

	// Data rows
	for i := 1; i < len(records); i++ {
		row := records[i]
		// Pad or trim if column count differs
		for len(row) < len(headers) {
			row = append(row, "")
		}
		if len(row) > len(headers) {
			row = row[:len(headers)]
		}
		sb.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}

	return sb.String(), nil
}

// ExtractPlainText parses arbitrary plain text or line-oriented streams.
func ExtractPlainText(r io.Reader) (string, error) {
	var sb strings.Builder
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
		sb.WriteString("\n")
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return sb.String(), nil
}
