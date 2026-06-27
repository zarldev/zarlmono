package retrieval

import (
	"fmt"
	"strings"

	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
)

// FormatOptions controls how retrieved documents are rendered into context.
type FormatOptions struct {
	Title      string
	MaxDocs    int
	MaxRunes   int
	ShowScores bool
}

// FormatDocuments renders retrieved documents as a compact prompt fragment.
func FormatDocuments(docs []airetrieval.Document, opts FormatOptions) string {
	if len(docs) == 0 {
		return ""
	}
	limit := opts.MaxDocs
	if limit <= 0 || limit > len(docs) {
		limit = len(docs)
	}
	title := opts.Title
	if title == "" {
		title = "Retrieved context"
	}
	var b strings.Builder
	b.WriteString(title)
	b.WriteString(":\n")
	for i := range limit {
		doc := docs[i]
		text := clipRunes(strings.TrimSpace(doc.Text), opts.MaxRunes)
		if text == "" {
			continue
		}
		if opts.ShowScores {
			fmt.Fprintf(&b, "[%d score=%.4f] %s\n", i+1, doc.Score, text)
		} else {
			fmt.Fprintf(&b, "[%d] %s\n", i+1, text)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func clipRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
