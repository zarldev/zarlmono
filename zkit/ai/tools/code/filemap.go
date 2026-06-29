package code

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/sourcecode"
)

// FileMapTool builds a deterministic, syntax-aware outline for source files.
type FileMapTool struct {
	ws                    Workspace
	allowOutsideWorkspace bool
}

// FileMapArgs configures the syntax-aware file map.
type FileMapArgs struct {
	// Root scopes the walk to a subtree. Empty means the workspace root.
	Root string `json:"root,omitempty" doc:"Optional sub-tree to scan, relative to the workspace. Empty = workspace root."`
	// Pattern selects files under Root using the same doublestar glob semantics as glob. Empty defaults to *.go.
	Pattern string `json:"pattern,omitempty" doc:"Glob pattern for files to map. Empty defaults to *.go. Bare basenames match anywhere under root."`
	// IncludeTests includes *_test.go files. Default false.
	IncludeTests bool `json:"include_tests,omitempty" doc:"Include Go test files (*_test.go). Default false."`
	// MaxFiles caps the number of files parsed. Default 200.
	MaxFiles int `json:"max_files,omitempty" doc:"Cap on parsed files. Default 200."`
	// Output selects labelled plaintext or JSON. Empty means labelled.
	Output tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default) or \"json\"."`
}

// NewFileMapTool returns a deterministic source-outline tool bound to ws.
func NewFileMapTool(ws Workspace, opts ...ReadOption) *FileMapTool {
	var policy readPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return &FileMapTool{ws: ws, allowOutsideWorkspace: policy.allowOutsideWorkspace}
}

// Definition advertises file_map as a read-only AST outline tool.
func (t *FileMapTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameFileMap,
		Description: "Build a deterministic syntax-aware map of source files so the model can understand code structure without reading full bodies. " +
			"Go files use go/parser+go/ast and include package, imports, top-level const/var/type declarations, funcs, methods, line ranges, and signatures. " +
			"This is a read-only alternative to LSP symbols; it does not require a language server.",
		Parameters: tools.SchemaFor[FileMapArgs](),
	}
}

const fileMapDefaultMaxFiles = 200

type fileMapPayload struct {
	Root      string         `json:"root,omitempty"`
	Pattern   string         `json:"pattern"`
	Files     []fileMapFile  `json:"files"`
	Truncated bool           `json:"truncated,omitempty"`
	Errors    []fileMapError `json:"errors,omitempty"`
}

type fileMapFile struct {
	Path    string        `json:"path"`
	Package string        `json:"package,omitempty"`
	Imports []string      `json:"imports,omitempty"`
	Decls   []fileMapDecl `json:"decls,omitempty"`
}

type fileMapDecl struct {
	Kind      sourcecode.SymbolKind `json:"kind"`
	Name      string                `json:"name"`
	Signature string                `json:"signature,omitempty"`
	Line      int                   `json:"line"`
	EndLine   int                   `json:"end_line"`
}

type fileMapError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// FileMapResult is file_map's structured result and model-facing renderer.
type FileMapResult struct {
	Payload fileMapPayload
	Output  tools.OutputFormat
}

// String renders either compact labelled text or JSON.
func (r FileMapResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(r.Payload)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderFileMap(r.Payload)
}

// Execute parses matching files and returns their deterministic outlines.
func (t *FileMapTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args FileMapArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if strings.TrimSpace(args.Pattern) == "" {
		args.Pattern = "*.go"
	}
	maxFiles := args.MaxFiles
	if maxFiles <= 0 {
		maxFiles = fileMapDefaultMaxFiles
	}

	globArgs := GlobArgs{Pattern: args.Pattern, Root: args.Root, MaxResults: maxFiles + 1}
	rootAbs, _, err := resolveGlob(t.ws, t.allowOutsideWorkspace, globArgs)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	matches, _, err := walkGlobMatches(ctx, t.ws, globArgs, rootAbs, maxFiles+1)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("file_map", fmt.Errorf("walk: %w", err))), nil
	}

	payload := fileMapPayload{Root: args.Root, Pattern: args.Pattern}
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
		if len(payload.Files) >= maxFiles {
			payload.Truncated = true
			break
		}
		abs := filepath.Join(rootAbs, filepath.FromSlash(match.Path))
		displayPath := displayPath(t.ws, abs, match.Path)
		mapped, mapErr := t.mapGoFile(abs, displayPath)
		if mapErr != nil {
			payload.Errors = append(payload.Errors, fileMapError{Path: displayPath, Error: mapErr.Error()})
			continue
		}
		payload.Files = append(payload.Files, mapped)
	}

	return tools.Success(call.ID, FileMapResult{Payload: payload, Output: args.Output}), nil
}

func (t *FileMapTool) mapGoFile(abs, displayPath string) (fileMapFile, error) {
	info, err := t.ws.StatPath(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fileMapFile{}, tools.NotFound("file_map", fmt.Sprintf("%q does not exist", displayPath))
		}
		return fileMapFile{}, fmt.Errorf("stat: %w", err)
	}
	if info.Size() > readMaxBytes {
		return fileMapFile{}, tools.Validation("file_map", fmt.Sprintf("%q: file too large (%d bytes, max %d)", displayPath, info.Size(), readMaxBytes))
	}
	data, err := t.ws.ReadFilePath(abs)
	if err != nil {
		return fileMapFile{}, err
	}

	parsed, err := sourcecode.GoParser{}.Parse(displayPath, data)
	if err != nil {
		return fileMapFile{}, err
	}
	mapped := fileMapFile{Path: displayPath, Package: parsed.Package}
	for _, imp := range parsed.Imports {
		prefix := ""
		if imp.Name != "" {
			prefix = imp.Name + " "
		}
		mapped.Imports = append(mapped.Imports, prefix+imp.Path)
	}
	for _, sym := range parsed.Symbols {
		name := sym.Name
		if sym.Kind == sourcecode.SymbolKindMethod && sym.Receiver != "" {
			name = "(" + sym.Receiver + ")." + name
		}
		mapped.Decls = append(mapped.Decls, fileMapDecl{
			Kind:      sym.Kind,
			Name:      name,
			Signature: sym.Signature,
			Line:      sym.StartLine,
			EndLine:   sym.EndLine,
		})
	}
	return mapped, nil
}

func displayPath(ws Workspace, abs, fallback string) string {
	if rel, err := filepath.Rel(ws.Root(), abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(fallback)
}

func renderFileMap(payload fileMapPayload) string {
	var b strings.Builder
	header := fmt.Sprintf("file_map: %d file(s)  pattern: %s", len(payload.Files), payload.Pattern)
	if payload.Root != "" {
		header += "  root: " + payload.Root
	}
	if payload.Truncated {
		header += "  (truncated)"
	}
	b.WriteString(header)
	b.WriteByte('\n')
	for _, file := range payload.Files {
		fmt.Fprintf(&b, "\n%s  package %s\n", file.Path, file.Package)
		if len(file.Imports) > 0 {
			b.WriteString("  imports: ")
			b.WriteString(strings.Join(file.Imports, ", "))
			b.WriteByte('\n')
		}
		for _, decl := range file.Decls {
			fmt.Fprintf(&b, "  L%d-L%d %s %s", decl.Line, decl.EndLine, decl.Kind, decl.Name)
			if decl.Signature != "" {
				b.WriteString(" :: ")
				b.WriteString(decl.Signature)
			}
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

var _ tools.Tool = (*FileMapTool)(nil)
