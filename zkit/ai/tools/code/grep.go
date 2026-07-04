package code

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const grepDefaultMaxResults = 100

// GrepTool wraps ripgrep and returns structured matches. The output field
// selects the model-facing rendering (labelled plaintext or JSON); the
// structured hits are identical either way.
type GrepTool struct {
	ws                    Workspace
	allowOutsideWorkspace bool
}

// GrepArgs is the typed argument struct GrepTool.Execute decodes
// into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type GrepArgs struct {
	Pattern         string             `json:"pattern" doc:"Regular expression."`
	Path            string             `json:"path,omitempty" doc:"Subpath inside workspace (default = workspace root)."`
	Glob            string             `json:"glob,omitempty" doc:"Glob filter (e.g. *.go)."`
	CaseInsensitive bool               `json:"case_insensitive,omitempty" doc:"Case-insensitive match."`
	MaxResults      int                `json:"max_results,omitempty" doc:"Cap on returned matches (default 100)."`
	Output          tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, hits grouped by file) or \"json\"."`
}

// NewGrepTool returns the content-search tool bound to ws.
func NewGrepTool(ws Workspace, opts ...ReadOption) *GrepTool {
	var policy readPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return &GrepTool{ws: ws, allowOutsideWorkspace: policy.allowOutsideWorkspace}
}

// Definition advertises grep with pattern (required), path, glob,
// case_insensitive, max_results (default 100), and a labeled|json
// output enum; searching never mutates.
func (t *GrepTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameGrep,
		Description: "Search workspace contents with ripgrep. Hits are grouped by file as `LINE: text` rows " +
			"under each path (set output=\"json\" for a JSON [{file, line, text}] array instead). " +
			"Honors .gitignore by default. Use `glob` for path-only enumeration and `read` to fetch full file contents.",
		Parameters: tools.SchemaFor[GrepArgs](),
	}
}

// GrepHit is one match in a grep result: the file, line number, and the
// matched line's text.
type GrepHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// GrepResult is grep's structured Data: the matches, whether the max-results
// cap truncated them, and the call inputs needed to render. It is the tool's
// typed payload — a consumer (the TUI) renders from Hits directly instead of
// re-parsing a string, while the model sees String(): labelled plaintext or a
// JSON array, per the requested Output.
type GrepResult struct {
	Hits       []GrepHit
	Truncated  bool
	Output     tools.OutputFormat
	Pattern    string
	Path       string
	Glob       string
	MaxResults int
}

// String renders the model-facing form for the requested output mode.
func (r GrepResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(r.Hits)
		if err != nil {
			return "[]"
		}
		return string(b)
	}
	return renderGrepLabeled(r)
}

// Execute requires ripgrep on PATH and a non-empty pattern, scopes the
// search to the resolved path (workspace root by default), and caps
// hits at max_results (grepDefaultMaxResults = 100 when unset). rg's
// exit 1 on zero matches is treated as success with an empty hit list.
func (t *GrepTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[GrepArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	hits, truncated, err := runGrep(ctx, t.ws, t.allowOutsideWorkspace, args)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = grepDefaultMaxResults
	}
	return tools.Success(call.ID, GrepResult{
		Hits:       hits,
		Truncated:  truncated,
		Output:     args.Output.Resolve(),
		Pattern:    args.Pattern,
		Path:       args.Path,
		Glob:       args.Glob,
		MaxResults: maxResults,
	}), nil
}

// renderGrepLabeled formats a result as the canonical labelled-output
// shape — one header line + a per-file group (file path indented two
// spaces, each match indented four spaces as `LINE: text`).
//
// Why grouped by file: ripgrep and every IDE search tool render this
// way, so the model's training prior is strongest on this form. It
// also de-duplicates the file path across same-file matches, which
// is where the token win lives for a noisy pattern.
func renderGrepLabeled(r GrepResult) string {
	var b strings.Builder
	header := fmt.Sprintf("matches: %d", len(r.Hits))
	if r.Truncated {
		header += fmt.Sprintf(" (truncated at cap %d — pass a higher max_results or narrow the pattern for more)", r.MaxResults)
	}
	header += fmt.Sprintf("  pattern: %s", r.Pattern)
	if r.Path != "" {
		header += fmt.Sprintf("  path: %s", r.Path)
	}
	if r.Glob != "" {
		header += fmt.Sprintf("  glob: %s", r.Glob)
	}
	b.WriteString(header)
	b.WriteString("\n")
	if len(r.Hits) == 0 {
		b.WriteString("(no matches)")
		return b.String()
	}
	currentFile := ""
	for _, h := range r.Hits {
		if h.File != currentFile {
			b.WriteString("  ")
			b.WriteString(h.File)
			b.WriteString("\n")
			currentFile = h.File
		}
		b.WriteString("    ")
		fmt.Fprintf(&b, "%d: %s\n", h.Line, h.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseGrepJSON walks ripgrep's --json event stream and collects up to
// maxResults match events into GrepHit rows. Returns the collected hits
// and a flag indicating whether the cap was hit.
func parseGrepJSON(out []byte, maxResults int) ([]GrepHit, bool) {
	var hits []GrepHit
	truncated := false
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if len(hits) >= maxResults {
			truncated = true
			break
		}
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				LineNumber int `json:"line_number"`
				Lines      struct {
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "match" {
			continue
		}
		hits = append(hits, GrepHit{
			File: strings.TrimPrefix(ev.Data.Path.Text, "./"),
			Line: ev.Data.LineNumber,
			Text: strings.TrimRight(ev.Data.Lines.Text, "\n"),
		})
	}
	return hits, truncated
}

// runGrep validates args, invokes ripgrep, and parses the JSON output.
// Returns the structured hits and whether the max-results cap truncated them.
func runGrep(ctx context.Context, ws Workspace, allowOutsideWorkspace bool, args GrepArgs) ([]GrepHit, bool, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false, tools.Fatal("grep", fmt.Errorf("ripgrep (rg) not installed: %w", err))
	}
	if args.Pattern == "" {
		return nil, false, tools.Validation("grep", "pattern required")
	}
	target := "."
	if args.Path != "" {
		abs, err := ws.ResolveForRead(args.Path, allowOutsideWorkspace)
		if err != nil {
			return nil, false, tools.Permission("grep", err.Error())
		}
		if ws.contains(abs) {
			target, err = ws.RelToRoot(abs)
			if err != nil {
				return nil, false, tools.Permission("grep", err.Error())
			}
		} else {
			target = abs
		}
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = grepDefaultMaxResults
	}

	rgArgs := []string{"--json", "--no-messages"}
	if args.CaseInsensitive {
		rgArgs = append(rgArgs, "-i")
	}
	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}
	rgArgs = append(rgArgs, "--", args.Pattern, target)

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	cmd.Dir = ws.Root()
	out, _ := cmd.Output() // rg exits 1 when no matches; ignore the error

	hits, truncated := parseGrepJSON(out, maxResults)
	return hits, truncated, nil
}
