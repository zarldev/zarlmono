package code

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// LsTool lists workspace directory entries. Hidden entries (starting with
// ".") are excluded by default. The output field selects the model-facing
// rendering (labelled plaintext or JSON); the structured entries are
// identical either way.
type LsTool struct{ ws Workspace }

// LsArgs is the typed argument struct LsTool.Execute decodes into
// via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type LsArgs struct {
	Path       string             `json:"path,omitempty" doc:"Directory inside workspace (default = root)."`
	ShowHidden bool               `json:"show_hidden,omitempty" doc:"Include dotfiles."`
	Output     tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, one entry per line with size and type) or \"json\"."`
}

// NewLsTool returns the directory-listing tool bound to ws.
func NewLsTool(ws Workspace) *LsTool { return &LsTool{ws: ws} }

// Definition advertises ls with optional path, show_hidden, and a
// labeled|json output enum; listing never mutates.
func (t *LsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameLs,
		Description: "List a workspace directory (one level, non-recursive). Returns labelled plaintext " +
			"(one entry per line, with size and type); set output=\"json\" for a [{name, type, size}] array " +
			"instead. Dotfiles are excluded unless `show_hidden: true`. Use `glob` for recursive path " +
			"enumeration and `read` to fetch contents.",
		Parameters: tools.SchemaFor[LsArgs](),
	}
}

// Entry-type values carried in lsEntry.Type and surfaced in the JSON
// output. Named so the classifier and the renderer agree on the spelling.
const (
	lsTypeFile    = "file"
	lsTypeDir     = "dir"
	lsTypeSymlink = "symlink"
)

type lsEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// LsResult is ls's structured Data: the directory entries plus the call
// inputs needed to render. A consumer (the TUI) renders from Entries
// directly instead of re-parsing a string, while the model sees String():
// labelled plaintext or a JSON array, per the requested Output.
type LsResult struct {
	Entries    []lsEntry
	Path       string
	ShowHidden bool
	Output     tools.OutputFormat
}

// String renders the model-facing form for the requested output mode.
func (r LsResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(r.Entries)
		if err != nil {
			return "[]"
		}
		return string(b)
	}
	return renderLsLabeled(r.Entries, r.Path, r.ShowHidden)
}

// collectLsEntries resolves the target path, reads directory entries,
// filters hidden entries, and classifies each as file/dir/symlink.
// Entries are sorted by name so both output modes — and any consumer
// rendering from the typed slice — see a stable order. Returns the entry
// list and a display path for the result message.
func collectLsEntries(ws Workspace, args LsArgs) ([]lsEntry, string, error) {
	target := ws.Root()
	displayPath := "."
	if args.Path != "" && args.Path != "." {
		abs, err := ws.Resolve(args.Path)
		if err != nil {
			return nil, "", tools.Permission("ls", err.Error())
		}
		target = abs
		displayPath = args.Path
	}

	entries, err := ws.ReadDirInRoot(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", tools.NotFound("ls", fmt.Sprintf("%q does not exist", displayPath))
		}
		return nil, "", tools.Fatal("ls", fmt.Errorf("%q: %w", displayPath, err))
	}

	out := make([]lsEntry, 0, len(entries))
	for _, e := range entries {
		if !args.ShowHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := lsTypeFile
		switch {
		case e.IsDir():
			typ = lsTypeDir
		case info.Mode()&os.ModeSymlink != 0:
			typ = lsTypeSymlink
		}
		out = append(out, lsEntry{Name: e.Name(), Type: typ, Size: info.Size()})
	}
	slices.SortFunc(out, func(a, b lsEntry) int { return cmp.Compare(a.Name, b.Name) })
	return out, displayPath, nil
}

// Execute resolves the directory (workspace root when path is empty),
// reads a single level only, skips dotfiles unless show_hidden,
// classifies entries as file/dir/symlink, and returns them sorted by
// name in the requested output format.
func (t *LsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args LsArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	entries, displayPath, err := collectLsEntries(t.ws, args)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	return tools.Success(call.ID, LsResult{
		Entries:    entries,
		Path:       displayPath,
		ShowHidden: args.ShowHidden,
		Output:     args.Output.Resolve(),
	}), nil
}

// renderLsLabeled formats entries as the canonical labelled-output shape:
// one header line (count + path + optional show_hidden flag) followed
// by one indented row per entry. Width-aligned so the size column lines
// up visually for the model — easier to scan than ragged columns.
func renderLsLabeled(entries []lsEntry, displayPath string, showHidden bool) string {
	var b strings.Builder
	header := fmt.Sprintf("entries: %d  path: %s", len(entries), displayPath)
	if showHidden {
		header += "  (showing hidden)"
	}
	b.WriteString(header)
	b.WriteString("\n")
	if len(entries) == 0 {
		b.WriteString("(empty)")
		return b.String()
	}
	nameWidth := 0
	for _, e := range entries {
		// Display width, not byte count: filesystem entries routinely
		// carry CJK (2 cols), emoji (2 cols), or accented chars (1 col,
		// multi-byte). runewidth.StringWidth gives display cells — the
		// canonical column-width primitive, charm-free.
		n := runewidth.StringWidth(e.Name)
		if e.Type == lsTypeDir {
			n++ // trailing slash on dirs
		}
		if n > nameWidth {
			nameWidth = n
		}
	}
	for _, e := range entries {
		name := e.Name
		if e.Type == lsTypeDir {
			name += "/"
		}
		pad := strings.Repeat(" ", nameWidth-runewidth.StringWidth(name))
		b.WriteString("  ")
		b.WriteString(name)
		b.WriteString(pad)
		b.WriteString("  ")
		switch e.Type {
		case lsTypeDir:
			b.WriteString("(dir)")
		case lsTypeSymlink:
			b.WriteString(formatBytes(e.Size))
			b.WriteString("  (symlink)")
		default:
			b.WriteString(formatBytes(e.Size))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
