package code

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/sourcecode"
)

// RetrieveCodeTool performs deterministic lexical retrieval over syntax chunks.
type RetrieveCodeTool struct {
	ws                    Workspace
	allowOutsideWorkspace bool
}

// RetrieveCodeArgs configures deterministic code retrieval.
type RetrieveCodeArgs struct {
	// Query is tokenized and matched against syntax chunks. Required.
	Query string `json:"query" doc:"Search query. Tokens are matched deterministically against paths, symbol metadata, and chunk text."`
	// Root scopes the scan to a subtree. Empty means the workspace root.
	Root string `json:"root,omitempty" doc:"Optional sub-tree to scan, relative to the workspace. Empty = workspace root."`
	// Pattern selects files under Root using glob semantics. Empty defaults to *.go.
	Pattern string `json:"pattern,omitempty" doc:"Glob pattern for files to retrieve from. Empty defaults to *.go."`
	// IncludeTests includes *_test.go files. Default false.
	IncludeTests bool `json:"include_tests,omitempty" doc:"Include Go test files (*_test.go). Default false."`
	// Limit caps returned chunks. Default 8.
	Limit int `json:"limit,omitempty" doc:"Maximum chunks to return. Default 8."`
	// MaxFiles caps the number of files parsed. Default 500.
	MaxFiles int `json:"max_files,omitempty" doc:"Cap on parsed files. Default 500."`
	// MaxBytesPerChunk caps rendered source text per chunk. Default 12000.
	MaxBytesPerChunk int `json:"max_bytes_per_chunk,omitempty" doc:"Cap rendered source bytes per chunk. Default 12000."`
	// Output selects labelled plaintext or JSON. Empty means labelled.
	Output tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default) or \"json\"."`
}

// NewRetrieveCodeTool returns a deterministic retrieval tool bound to ws.
func NewRetrieveCodeTool(ws Workspace, opts ...ReadOption) *RetrieveCodeTool {
	var policy readPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return &RetrieveCodeTool{ws: ws, allowOutsideWorkspace: policy.allowOutsideWorkspace}
}

// Definition advertises retrieve_code as a read-only deterministic retrieval tool.
func (t *RetrieveCodeTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameRetrieveCode,
		Description: "Deterministically retrieve relevant source chunks without embeddings or LSP. " +
			"Go files are split by SyntaxChunker (go/parser+go/ast) into whole funcs, methods, and types, then ranked by stable lexical matching against query tokens, paths, symbols, and source text.",
		Parameters: tools.SchemaFor[RetrieveCodeArgs](),
	}
}

const (
	retrieveCodeDefaultLimit            = 8
	retrieveCodeDefaultMaxFiles         = 500
	retrieveCodeDefaultMaxBytesPerChunk = 12 * 1024
)

type retrieveCodePayload struct {
	Query     string              `json:"query"`
	Root      string              `json:"root,omitempty"`
	Pattern   string              `json:"pattern"`
	Chunks    []retrieveCodeChunk `json:"chunks"`
	Truncated bool                `json:"truncated,omitempty"`
	Errors    []fileMapError      `json:"errors,omitempty"`
}

type retrieveCodeChunk struct {
	Path      string                `json:"path"`
	ID        string                `json:"id,omitempty"`
	Kind      sourcecode.SymbolKind `json:"kind,omitempty"`
	Name      string                `json:"name,omitempty"`
	Receiver  string                `json:"receiver,omitempty"`
	StartLine int                   `json:"start_line,omitempty"`
	EndLine   int                   `json:"end_line,omitempty"`
	Score     int                   `json:"score"`
	Text      string                `json:"text"`
}

// RetrieveCodeResult is retrieve_code's structured result and renderer.
type RetrieveCodeResult struct {
	Payload retrieveCodePayload
	Output  tools.OutputFormat
}

// String renders labelled text by default or JSON when requested.
func (r RetrieveCodeResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(r.Payload)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderRetrieveCode(r.Payload)
}

// Execute scans files, syntax-chunks them, ranks chunks deterministically, and returns the top matches.
func (t *RetrieveCodeTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args RetrieveCodeArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return tools.Failure(call.ID, tools.Validation("retrieve_code", "query required")), nil
	}
	if strings.TrimSpace(args.Pattern) == "" {
		args.Pattern = "*.go"
	}
	limit := args.Limit
	if limit <= 0 {
		limit = retrieveCodeDefaultLimit
	}
	maxFiles := args.MaxFiles
	if maxFiles <= 0 {
		maxFiles = retrieveCodeDefaultMaxFiles
	}
	maxBytes := args.MaxBytesPerChunk
	if maxBytes <= 0 {
		maxBytes = retrieveCodeDefaultMaxBytesPerChunk
	}

	globArgs := GlobArgs{Pattern: args.Pattern, Root: args.Root, MaxResults: maxFiles + 1}
	rootAbs, _, err := resolveGlob(t.ws, t.allowOutsideWorkspace, globArgs)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	matches, _, err := walkGlobMatches(ctx, t.ws, globArgs, rootAbs, maxFiles+1)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("retrieve_code", fmt.Errorf("walk: %w", err))), nil
	}

	payload := retrieveCodePayload{Query: args.Query, Root: args.Root, Pattern: args.Pattern}
	docs := make([]airetrieval.SourceDocument, 0, min(len(matches), maxFiles))
	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if match.Dir {
			continue
		}
		if !args.IncludeTests && strings.HasSuffix(match.Path, "_test.go") {
			continue
		}
		if len(docs) >= maxFiles {
			payload.Truncated = true
			break
		}
		abs := filepath.Join(rootAbs, filepath.FromSlash(match.Path))
		display := displayPath(t.ws, abs, match.Path)
		data, readErr := t.readRetrievalFile(abs, display)
		if readErr != nil {
			payload.Errors = append(payload.Errors, fileMapError{Path: display, Error: readErr.Error()})
			continue
		}
		docs = append(docs, airetrieval.SourceDocument{
			ID:   airetrieval.DocumentID(display),
			Text: string(data),
			Path: display,
			Ext:  strings.TrimPrefix(filepath.Ext(display), "."),
		})
	}

	chunks, err := (airetrieval.SyntaxChunker{}).ChunkSource(ctx, docs)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("retrieve_code", err)), nil
	}
	payload.Chunks = rankRetrieveCode(args.Query, chunks, limit, maxBytes)
	return tools.Success(call.ID, RetrieveCodeResult{Payload: payload, Output: args.Output}), nil
}

func (t *RetrieveCodeTool) readRetrievalFile(abs, display string) ([]byte, error) {
	info, err := t.ws.StatPath(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tools.NotFound("retrieve_code", fmt.Sprintf("%q does not exist", display))
		}
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.Size() > readMaxBytes {
		return nil, tools.Validation("retrieve_code", fmt.Sprintf("%q: file too large (%d bytes, max %d)", display, info.Size(), readMaxBytes))
	}
	return t.ws.ReadFilePath(abs)
}

type scoredChunk struct {
	chunk retrieveCodeChunk
	path  string
	idx   int
}

func rankRetrieveCode(query string, docs []airetrieval.SourceChunk, limit, maxBytes int) []retrieveCodeChunk {
	tokens := queryTokens(query)
	scored := make([]scoredChunk, 0, len(docs))
	for i, doc := range docs {
		chunk := buildRetrieveCodeChunk(doc, maxBytes)
		chunk.Score = scoreRetrieveCodeChunk(tokens, chunk)
		if chunk.Score <= 0 {
			continue
		}
		scored = append(scored, scoredChunk{chunk: chunk, path: chunk.Path, idx: i})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i].chunk, scored[j].chunk
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return scored[i].idx < scored[j].idx
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]retrieveCodeChunk, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.chunk)
	}
	return out
}

func buildRetrieveCodeChunk(doc airetrieval.SourceChunk, maxBytes int) retrieveCodeChunk {
	text := doc.Text
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[:maxBytes] + fmt.Sprintf("\n... [truncated: %d more bytes]", len(doc.Text)-maxBytes)
	}
	return retrieveCodeChunk{
		Path:      doc.Path,
		ID:        string(doc.ID),
		Kind:      doc.Kind,
		Name:      doc.Symbol.Name,
		Receiver:  doc.Receiver,
		StartLine: doc.Symbol.StartLine,
		EndLine:   doc.Symbol.EndLine,
		Text:      text,
	}
}

func scoreRetrieveCodeChunk(tokens []string, chunk retrieveCodeChunk) int {
	hayPath := strings.ToLower(chunk.Path)
	hayName := strings.ToLower(chunk.Name + " " + chunk.Receiver + " " + string(chunk.Kind))
	hayText := strings.ToLower(chunk.Text)
	score := 0
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if strings.Contains(hayPath, tok) {
			score += 12
		}
		if strings.Contains(hayName, tok) {
			score += 20
		}
		score += min(strings.Count(hayText, tok), 12)
	}
	return score
}

var queryTokenRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

func queryTokens(query string) []string {
	raw := queryTokenRe.FindAllString(strings.ToLower(query), -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = strings.TrimFunc(tok, func(r rune) bool { return r == '_' || unicode.IsSpace(r) })
		if len(tok) < 2 {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}

func renderRetrieveCode(payload retrieveCodePayload) string {
	var b strings.Builder
	header := fmt.Sprintf("retrieve_code: %d chunk(s)  query: %q  pattern: %s", len(payload.Chunks), payload.Query, payload.Pattern)
	if payload.Root != "" {
		header += "  root: " + payload.Root
	}
	if payload.Truncated {
		header += "  (file scan truncated)"
	}
	b.WriteString(header)
	b.WriteByte('\n')
	for i, chunk := range payload.Chunks {
		fmt.Fprintf(&b, "\n[%d] %s:L%d-L%d score=%d %s %s", i+1, chunk.Path, chunk.StartLine, chunk.EndLine, chunk.Score, chunk.Kind, chunk.Name)
		if chunk.Receiver != "" {
			fmt.Fprintf(&b, " receiver=%s", chunk.Receiver)
		}
		b.WriteString("\n")
		b.WriteString(chunk.Text)
		if !strings.HasSuffix(chunk.Text, "\n") {
			b.WriteByte('\n')
		}
	}
	if len(payload.Errors) > 0 {
		b.WriteString("\nerrors:\n")
		for _, e := range payload.Errors {
			fmt.Fprintf(&b, "  %s: %s\n", e.Path, e.Error)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

var _ tools.Tool = (*RetrieveCodeTool)(nil)
