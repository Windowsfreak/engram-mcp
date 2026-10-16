package chunker

import (
	"unicode"
)

// EstimateTokens calculates an approximate token count for text.
// nomic-embed-text-v2-moe uses subword tokenization where 1 token is ~3.5-4 characters in German/English.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	charCount := len([]rune(text))
	wordCount := 0
	inWord := false

	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			wordCount++
		}
	}

	// Heuristic combination: 1 word is typically 1.3 tokens; 4 chars is ~1 token.
	byChars := (charCount + 3) / 4
	byWords := (wordCount * 13) / 10

	if byChars > byWords {
		return byChars
	}
	return byWords
}
