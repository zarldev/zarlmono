package main

import (
	"compress/bzip2"
	"context"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/wiki"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

func main() {
	dump := flag.String("dump", "", "path to Wikipedia XML dump (.xml or .xml.bz2) (required)")
	qdrantURL := flag.String("qdrant", "http://localhost:6333", "Qdrant URL")
	embedURL := flag.String("embed-url", "http://localhost:11434/v1", "OpenAI-compatible /v1 embeddings endpoint")
	embedModel := flag.String("embed-model", "nomic-embed-text", "embedding model")
	batch := flag.Int("batch", 50, "points per upsert batch")
	chunkSize := flag.Int("chunk-size", 500, "approximate words per chunk")
	maxArticles := flag.Int("max-articles", 0, "max articles to process (0 = all)")
	flag.Parse()

	if *dump == "" {
		log.Fatal("-dump is required")
	}

	ctx := context.Background()

	qc := qdrant.NewClient(*qdrantURL)
	embedder := service.NewOpenAIEmbedder(*embedURL, *embedModel)

	// Embed a probe to discover vector size.
	probe, err := embedder.Embed(ctx, "probe")
	if err != nil {
		log.Fatalf("probe embed: %v", err)
	}
	vectorSize := len(probe)

	if err := qc.EnsureCollection(ctx, wiki.Collection, vectorSize); err != nil {
		log.Fatalf("ensure collection: %v", err)
	}

	f, err := os.Open(*dump)
	if err != nil {
		log.Fatalf("open dump: %v", err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(*dump, ".bz2") {
		r = bzip2.NewReader(f)
	}

	var (
		pending      []qdrant.Point
		articleCount int
		pointCount   int
	)

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := qc.Upsert(ctx, wiki.Collection, pending); err != nil {
			return fmt.Errorf("upsert batch: %w", err)
		}
		pointCount += len(pending)
		pending = pending[:0]
		return nil
	}

	decoder := xml.NewDecoder(r)
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatalf("xml decode: %v", err)
		}

		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "page" {
			continue
		}

		var page xmlPage
		if err := decoder.DecodeElement(&page, &se); err != nil {
			log.Printf("decode page: %v", err)
			continue
		}

		if skipTitle(page.Title) {
			continue
		}

		text := stripMarkup(page.Revision.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}

		chunks := chunkText(text, *chunkSize)
		for i, chunk := range chunks {
			vec, err := embedder.Embed(ctx, chunk)
			if err != nil {
				log.Printf("embed %q chunk %d: %v", page.Title, i, err)
				continue
			}
			pending = append(pending, qdrant.Point{
				ID:     uuid.NewString(),
				Vector: vec,
				Payload: map[string]any{
					"title":   page.Title,
					"section": fmt.Sprintf("chunk_%d", i),
					"text":    chunk,
				},
			})
			if len(pending) >= *batch {
				if err := flush(); err != nil {
					log.Fatalf("%v", err)
				}
			}
		}

		articleCount++
		if articleCount%100 == 0 {
			log.Printf("processed %d articles, %d points upserted", articleCount, pointCount)
		}

		if *maxArticles > 0 && articleCount >= *maxArticles {
			break
		}
	}

	if err := flush(); err != nil {
		log.Fatalf("%v", err)
	}

	log.Printf("done: %d articles, %d points total", articleCount, pointCount)
}

// xmlPage mirrors the Wikipedia XML dump <page> structure we care about.
type xmlPage struct {
	Title    string `xml:"title"`
	Revision struct {
		Text string `xml:"text"`
	} `xml:"revision"`
}

var skipPrefixes = []string{
	"Wikipedia:", "Template:", "Category:", "File:", "Module:", "MediaWiki:",
}

func skipTitle(title string) bool {
	for _, p := range skipPrefixes {
		if strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}

// stripMarkup removes common wiki markup heuristically.
func stripMarkup(text string) string {
	// Remove <ref>...</ref> tags (possibly multiline).
	text = removeTag(text, "ref")

	// Remove {{...}} templates (nested-aware).
	text = removeTemplates(text)

	var sb strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip file/image links and table markup.
		if strings.HasPrefix(trimmed, "[[File:") ||
			strings.HasPrefix(trimmed, "[[Image:") ||
			strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, "!") {
			continue
		}
		// Convert [[link|text]] → text, [[link]] → link.
		trimmed = convertLinks(trimmed)
		// Remove bold/italic markers.
		trimmed = strings.ReplaceAll(trimmed, "'''", "")
		trimmed = strings.ReplaceAll(trimmed, "''", "")
		if trimmed == "" {
			continue
		}
		sb.WriteString(trimmed)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// removeTag removes all occurrences of <tag ...>...</tag> (simple, non-nested).
func removeTag(text, tag string) string {
	open := "<" + tag
	close := "</" + tag + ">"
	for {
		start := strings.Index(text, open)
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], close)
		if end == -1 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+len(close):]
	}
	return text
}

// removeTemplates removes {{ ... }} blocks, handling nesting.
func removeTemplates(text string) string {
	var sb strings.Builder
	depth := 0
	i := 0
	for i < len(text) {
		if i+1 < len(text) && text[i] == '{' && text[i+1] == '{' {
			depth++
			i += 2
			continue
		}
		if i+1 < len(text) && text[i] == '}' && text[i+1] == '}' {
			if depth > 0 {
				depth--
			}
			i += 2
			continue
		}
		if depth == 0 {
			sb.WriteByte(text[i])
		}
		i++
	}
	return sb.String()
}

// convertLinks turns [[A|B]] into B and [[A]] into A.
func convertLinks(text string) string {
	var sb strings.Builder
	for {
		start := strings.Index(text, "[[")
		if start == -1 {
			sb.WriteString(text)
			break
		}
		end := strings.Index(text[start:], "]]")
		if end == -1 {
			sb.WriteString(text)
			break
		}
		sb.WriteString(text[:start])
		inner := text[start+2 : start+end]
		if _, after, ok := strings.Cut(inner, "|"); ok {
			sb.WriteString(after)
		} else {
			sb.WriteString(inner)
		}
		text = text[start+end+2:]
	}
	return sb.String()
}

// chunkText splits text into word-based chunks with ~10% overlap.
func chunkText(text string, chunkSize int) []string {
	words := strings.Fields(text)
	if len(words) <= chunkSize {
		return []string{text}
	}

	overlap := max(chunkSize/10, 1)

	var chunks []string
	start := 0
	for start < len(words) {
		end := min(start+chunkSize, len(words))
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
		start = end - overlap
	}
	return chunks
}
