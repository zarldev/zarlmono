package code

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// ApplyPatchTool applies a Codex-style "stripped-down diff" patch
// across multiple files in the workspace. The patch grammar is the
// envelope-and-hunks format documented at
// openai/codex/codex-rs/apply-patch/apply_patch_tool_instructions.md
// — we implement it verbatim so models trained against it (GPT-5
// family, Claude) emit patches the parser accepts as-is.
//
// The tool buys two things over the existing edit/write tools:
//
//  1. Multi-file changes commit atomically — every file change is
//     staged in memory, and the commit phase only fires after every
//     hunk has applied cleanly. A bad hunk halts the whole patch
//     with no half-written files.
//  2. Models that know the format produce far more reliable diffs
//     than they do dictating search/replace strings, especially for
//     overlapping or sequence-sensitive edits.
type ApplyPatchTool struct{ ws Workspace }

// ApplyPatchArgs is the typed argument struct ApplyPatchTool.Execute
// decodes into via tools.DecodeArgs.
type ApplyPatchArgs struct {
	Patch string `json:"patch" doc:"The full patch text including the \x60*** Begin Patch\x60 / \x60*** End Patch\x60 envelope."`
}

// NewApplyPatchTool returns the unified-diff patch tool bound to ws.
func NewApplyPatchTool(ws Workspace) *ApplyPatchTool { return &ApplyPatchTool{ws: ws} }

// PatchPaths scans patch text for the file headers it touches and
// returns workspace-relative paths in declaration order. Add /
// Update / Delete each contribute their target path; an Update
// followed by "*** Move to:" contributes both the original and the
// destination so observability layers can diff both ends.
//
// Returns nil on a patch that doesn't parse — callers treat that as
// "no observable paths" and skip snapshotting. The full parser
// inside Execute will surface the syntax error to the model.
//
// Exposed for [pkg/agent/diffrecorder]: apply_patch can mutate multiple
// files in a single call, so the single-"path"-argument extractor
// the rest of the recordable tools share doesn't fit. The recorder
// calls this to enumerate the files it should snapshot before
// dispatching apply_patch.
func PatchPaths(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		var p string
		if _, path, ok := matchFileHeader(line); ok {
			p = path
		} else if after, ok0 := strings.CutPrefix(line, "*** Move to: "); ok0 {
			p = strings.TrimSpace(after)
		} else {
			continue
		}
		if p == "" {
			continue
		}
		// De-dup so an Add then Update of the same file (illegal
		// patch but cheap to defend against) doesn't double-snapshot.
		seen := slices.Contains(out, p)
		if !seen {
			out = append(out, p)
		}
	}
	return out
}

// Definition advertises apply_patch with the single patch parameter
// (the full *** Begin Patch / *** End Patch envelope); Mutates is true
// because a committed patch adds, updates, deletes, or moves workspace
// files.
func (t *ApplyPatchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameApplyPatch,
		Description: "Apply a Codex-style patch across one or more workspace files. Patch body uses the standard `*** Begin Patch` / `*** End Patch` envelope with Add / Update / Delete sections and `@@` hunks. Commits atomically — any hunk failure rolls back the whole patch. Best fit for multi-file changes; for single-line tweaks `edit` is simpler.",
		Parameters:  tools.SchemaFor[ApplyPatchArgs](),
		Mutates:     true,
	}
}

// Execute parses the patch, plans all file mutations, then commits
// them in one pass. Errors at any stage abort the whole operation.
func (t *ApplyPatchTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[ApplyPatchArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Patch == "" {
		return tools.Failure(call.ID, tools.Validation("apply_patch", "patch text is empty")), nil
	}
	p, err := parsePatch(args.Patch)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("apply_patch", fmt.Sprintf("parse: %v", err))), nil
	}
	plan, err := t.planPatch(p)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("apply_patch", err.Error())), nil
	}
	if err := t.commitPatch(plan); err != nil {
		return tools.Failure(call.ID, tools.Fatal("apply_patch", fmt.Errorf("commit: %w", err))), nil
	}
	return tools.Success(call.ID, plan.summary(), plan.effects()...), nil
}

// --- Parser ---

// fileOpKind is the action declared by an "*** {Add,Update,Delete} File: …"
// header inside the patch envelope.
type fileOpKind int

const (
	opAdd fileOpKind = iota
	opUpdate
	opDelete
)

// parsedFileOp is one element of a parsed patch. addLines is non-empty
// only for opAdd; hunks for opUpdate.
type parsedFileOp struct {
	kind     fileOpKind
	path     string
	moveTo   string // populated when an Update is followed by *** Move to:
	addLines []string
	hunks    []parsedHunk
}

// parsedHunk is one @@ block inside an Update file op. headerAnchors
// captures the optional `@@ <anchor>` text that lets the model
// disambiguate repeated snippets — there can be more than one, stacked
// for nested anchoring (e.g. `@@ class Foo` then `@@ def bar()`).
type parsedHunk struct {
	headerAnchors []string
	lines         []parsedHunkLine
	endOfFile     bool
}

type parsedHunkLine struct {
	kind byte // ' ', '-', '+'
	text string
}

// parsedPatch carries the full sequence of file ops in declaration
// order. Order matters because the model can refer to a file it just
// created earlier in the same patch.
type parsedPatch struct {
	ops []parsedFileOp
}

// parsePatch decodes the patch text into a parsedPatch. The grammar
// follows the spec verbatim:
//
//	Patch    := "*** Begin Patch" NEWLINE { FileOp } "*** End Patch" NEWLINE
//	FileOp   := AddFile | DeleteFile | UpdateFile
//	AddFile  := "*** Add File: " path NEWLINE { "+" line NEWLINE }
//	DeleteFile := "*** Delete File: " path NEWLINE
//	UpdateFile := "*** Update File: " path NEWLINE [ MoveTo ] { Hunk }
//	MoveTo   := "*** Move to: " newPath NEWLINE
//	Hunk     := "@@" [ header ] NEWLINE { HunkLine } [ "*** End of File" NEWLINE ]
//	HunkLine := (" " | "-" | "+") text NEWLINE
func parsePatch(text string) (parsedPatch, error) {
	lines := strings.Split(text, "\n")
	// strings.Split on a trailing newline leaves a blank tail; drop it
	// so the parser doesn't have to special-case the last index.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return parsedPatch{}, errors.New("patch is empty")
	}
	// Locate the envelope. Some models emit a blank line or a stray
	// comment before "*** Begin Patch"; tolerate it.
	begin := -1
	for i, l := range lines {
		if l == "*** Begin Patch" {
			begin = i
			break
		}
	}
	if begin < 0 {
		return parsedPatch{}, errors.New(`missing "*** Begin Patch" header`)
	}
	end := -1
	for i := len(lines) - 1; i > begin; i-- {
		if lines[i] == "*** End Patch" {
			end = i
			break
		}
	}
	if end < 0 {
		return parsedPatch{}, errors.New(`missing "*** End Patch" trailer`)
	}

	body := lines[begin+1 : end]
	var p parsedPatch
	i := 0
	for i < len(body) {
		line := body[i]
		kind, path, isHeader := matchFileHeader(line)
		switch {
		case isHeader && kind == opAdd:
			if path == "" {
				return parsedPatch{}, fmt.Errorf("line %d: Add File header has empty path", begin+1+i+1)
			}
			i++
			op := parsedFileOp{kind: opAdd, path: path}
			for i < len(body) && !isSectionHeader(body[i]) {
				if !strings.HasPrefix(body[i], "+") {
					return parsedPatch{}, fmt.Errorf(
						"line %d: Add File body must start with '+' (got %q)",
						begin+1+i+1,
						body[i],
					)
				}
				op.addLines = append(op.addLines, strings.TrimPrefix(body[i], "+"))
				i++
			}
			p.ops = append(p.ops, op)
		case isHeader && kind == opDelete:
			if path == "" {
				return parsedPatch{}, fmt.Errorf("line %d: Delete File header has empty path", begin+1+i+1)
			}
			p.ops = append(p.ops, parsedFileOp{kind: opDelete, path: path})
			i++
		case isHeader && kind == opUpdate:
			if path == "" {
				return parsedPatch{}, fmt.Errorf("line %d: Update File header has empty path", begin+1+i+1)
			}
			op := parsedFileOp{kind: opUpdate, path: path}
			i++
			// Optional Move to.
			if i < len(body) && strings.HasPrefix(body[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(body[i], "*** Move to: "))
				if op.moveTo == "" {
					return parsedPatch{}, fmt.Errorf("line %d: Move to header has empty target", begin+1+i+1)
				}
				i++
			}
			// One or more hunks.
			for i < len(body) && !isSectionHeader(body[i]) {
				if !strings.HasPrefix(body[i], "@@") {
					return parsedPatch{}, fmt.Errorf(
						"line %d: expected @@ hunk header in Update File %q (got %q)",
						begin+1+i+1,
						path,
						body[i],
					)
				}
				h := parsedHunk{}
				// Consume stacked @@ anchors (nested context cues).
				for i < len(body) && strings.HasPrefix(body[i], "@@") {
					anchor := strings.TrimSpace(strings.TrimPrefix(body[i], "@@"))
					if anchor != "" {
						h.headerAnchors = append(h.headerAnchors, anchor)
					}
					i++
				}
				// Read hunk body lines until next @@ / section header.
				for i < len(body) && !strings.HasPrefix(body[i], "@@") && !isSectionHeader(body[i]) {
					l := body[i]
					if l == "*** End of File" {
						h.endOfFile = true
						i++
						break
					}
					if l == "" {
						// A blank line inside a hunk is a context line —
						// treat as space-prefixed empty string. Models
						// often elide the leading space on truly empty
						// context lines.
						h.lines = append(h.lines, parsedHunkLine{kind: ' ', text: ""})
						i++
						continue
					}
					switch l[0] {
					case ' ', '-', '+':
						h.lines = append(h.lines, parsedHunkLine{kind: l[0], text: l[1:]})
					default:
						return parsedPatch{}, fmt.Errorf(
							"line %d: hunk line must start with ' ', '-', or '+' (got %q)",
							begin+1+i+1,
							l,
						)
					}
					i++
				}
				op.hunks = append(op.hunks, h)
			}
			if len(op.hunks) == 0 {
				return parsedPatch{}, fmt.Errorf("update File %q: no hunks", path)
			}
			p.ops = append(p.ops, op)
		case line == "":
			// Tolerate blank lines between file operations.
			i++
		default:
			// A line that opens with the directive marker but didn't match
			// a known header is almost always a malformed header (wrong
			// verb, missing path, stray colon) — point the model at the
			// grammar so it can self-correct instead of blindly retrying.
			if strings.HasPrefix(line, "***") {
				return parsedPatch{}, fmt.Errorf(
					"line %d: unrecognized patch directive %q — file headers must read "+
						"\"*** Update File: path\", \"*** Add File: path\", or "+
						"\"*** Delete File: path\" (the short forms \"*** Update path\" etc. "+
						"are also accepted)",
					begin+1+i+1, line)
			}
			return parsedPatch{}, fmt.Errorf("line %d: unexpected content outside file op: %q", begin+1+i+1, line)
		}
	}
	if len(p.ops) == 0 {
		return parsedPatch{}, errors.New("patch contains no file operations")
	}
	return p, nil
}

// isSectionHeader reports whether the line opens a new file op or
// closes the envelope. Used to terminate parsing of variable-length
// op bodies. Hunk and Add-body lines always carry a leading ' ', '-',
// or '+' marker, so a header form can't be confused with real file
// content at column 0.
func isSectionHeader(l string) bool {
	if l == "*** End Patch" {
		return true
	}
	_, _, ok := matchFileHeader(l)
	return ok
}

// fileHeaderForms pairs each file op with its canonical "File:" header
// and the "File:"-less alias models frequently emit. The canonical form
// is checked before the alias because the alias prefix ("*** Update ")
// is itself a prefix of the canonical one ("*** Update File: ").
var fileHeaderForms = []struct {
	kind      fileOpKind
	canonical string
	lenient   string
}{
	{opAdd, "*** Add File: ", "*** Add "},
	{opUpdate, "*** Update File: ", "*** Update "},
	{opDelete, "*** Delete File: ", "*** Delete "},
}

// matchFileHeader recognizes a file-op header line. It accepts both the
// canonical "*** Update File: path" grammar and the "File:"-less
// "*** Update path" form models commonly emit — the latter is
// unambiguous (no other patch directive shares those prefixes), so
// honoring it just saves a wasted retry. Returns the op kind, the
// trimmed target path, and whether the line was a header at all.
func matchFileHeader(line string) (fileOpKind, string, bool) {
	for _, f := range fileHeaderForms {
		if after, ok := strings.CutPrefix(line, f.canonical); ok {
			return f.kind, strings.TrimSpace(after), true
		}
		if after, ok := strings.CutPrefix(line, f.lenient); ok {
			return f.kind, strings.TrimSpace(after), true
		}
	}
	return 0, "", false
}

// --- Planner ---

// stagedFile is the post-patch state of a single file. write is the
// new content (or "" for delete). delete is true when the file should
// be removed.
type stagedFile struct {
	srcPath     string // workspace-relative source path
	dstPath     string // workspace-relative destination path (differs only when renamed)
	renamedFrom string // populated on rename destination entries
	delete      bool
	create      bool
	contents    string
}

// patchPlan is the resolved-but-not-yet-applied state mutations. Built
// in planPatch; consumed in commitPatch. Keeping plan/commit separate
// gives us a clean rollback point — any planning error means no disk
// writes happen.
type patchPlan struct {
	files []stagedFile
	// Each file op's index back to its source op, used for the human
	// summary string. Same length as files.
	summaryLines []string
}

func (p patchPlan) summary() string {
	return "applied patch: " + strings.Join(p.summaryLines, "; ")
}

func (p patchPlan) effects() []tools.Effect {
	renamedSources := map[string]struct{}{}
	for _, sf := range p.files {
		if sf.renamedFrom != "" {
			renamedSources[sf.renamedFrom] = struct{}{}
		}
	}
	effects := make([]tools.Effect, 0, len(p.files))
	for _, sf := range p.files {
		switch {
		case sf.renamedFrom != "":
			e := tools.NewFileEffect(tools.FileRename, sf.dstPath)
			e.File.FromPath = sf.renamedFrom
			e.File.BytesAfter = int64(len(sf.contents))
			effects = append(effects, e)
		case sf.delete:
			if _, renamed := renamedSources[sf.dstPath]; renamed {
				continue
			}
			effects = append(effects, tools.NewFileEffect(tools.FileDelete, sf.dstPath))
		case sf.create:
			e := tools.NewFileEffect(tools.FileCreate, sf.dstPath)
			e.File.BytesAfter = int64(len(sf.contents))
			effects = append(effects, e)
		default:
			e := tools.NewFileEffect(tools.FileModify, sf.dstPath)
			e.File.BytesAfter = int64(len(sf.contents))
			effects = append(effects, e)
		}
	}
	return effects
}

// planPatch resolves every op against the workspace + against
// previously planned ops within the same patch. A patch that
// `Add File: a` then `Update File: a` is legal — the update applies
// to the just-added content.
func (t *ApplyPatchTool) planPatch(p parsedPatch) (patchPlan, error) {
	// In-memory view of file contents during planning. Tracks both
	// disk reads and the result of earlier ops in this patch.
	type fileView struct {
		exists         bool
		originalExists bool
		contents       string
		renamedFrom    string
		// pathBound is the path the in-memory state should be written
		// back to at commit time (changes when a Move to renames).
		pathBound string
	}
	views := map[string]*fileView{}

	load := func(path string) (*fileView, error) {
		if v, ok := views[path]; ok {
			return v, nil
		}
		abs, err := t.ws.Resolve(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", path, err)
		}
		data, err := t.ws.ReadFileInRoot(abs)
		if err != nil {
			if os.IsNotExist(err) {
				views[path] = &fileView{exists: false, originalExists: false, pathBound: path}
				return views[path], nil
			}
			return nil, fmt.Errorf("read %q: %w", path, err)
		}
		views[path] = &fileView{exists: true, originalExists: true, contents: string(data), pathBound: path}
		return views[path], nil
	}

	for _, op := range p.ops {
		switch op.kind {
		case opAdd:
			v, err := load(op.path)
			if err != nil {
				return patchPlan{}, err
			}
			if v.exists {
				return patchPlan{}, fmt.Errorf("add File %q: already exists", op.path)
			}
			v.exists = true
			v.contents = joinAdd(op.addLines)
		case opDelete:
			v, err := load(op.path)
			if err != nil {
				return patchPlan{}, err
			}
			if !v.exists {
				return patchPlan{}, fmt.Errorf("delete File %q: does not exist", op.path)
			}
			v.exists = false
			v.contents = ""
		case opUpdate:
			v, err := load(op.path)
			if err != nil {
				return patchPlan{}, err
			}
			if !v.exists {
				return patchPlan{}, fmt.Errorf("update File %q: does not exist", op.path)
			}
			newContents, err := applyHunks(op.path, v.contents, op.hunks)
			if err != nil {
				return patchPlan{}, err
			}
			v.contents = newContents
			if op.moveTo != "" {
				// Rename: mark old path for deletion (only if the
				// rename target differs) and rebind the in-memory
				// state at the new path.
				newView, err := load(op.moveTo)
				if err != nil {
					return patchPlan{}, err
				}
				if newView.exists && op.moveTo != op.path {
					return patchPlan{}, fmt.Errorf(
						"update File %q: move to %q would overwrite existing file",
						op.path,
						op.moveTo,
					)
				}
				newView.exists = true
				newView.contents = v.contents
				newView.pathBound = op.moveTo
				if op.moveTo != op.path {
					newView.renamedFrom = op.path
					v.exists = false
					v.contents = ""
				}
			}
		}
	}

	// Convert views into ordered staged files. Order: paths in the
	// order ops referenced them (Go map iteration is randomised, so
	// we rebuild order from the ops list).
	seen := map[string]bool{}
	var plan patchPlan
	for _, op := range p.ops {
		paths := []string{op.path}
		if op.moveTo != "" {
			paths = append(paths, op.moveTo)
		}
		for _, path := range paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			v := views[path]
			if v == nil {
				continue
			}
			sf := stagedFile{
				srcPath:     path,
				dstPath:     path,
				renamedFrom: v.renamedFrom,
				contents:    v.contents,
			}
			switch {
			case !v.exists:
				sf.delete = true
				sf.contents = ""
			case v.exists:
				// `create` means the path was absent when first loaded and
				// exists in the staged post-image.
				sf.create = !v.originalExists
			}
			plan.files = append(plan.files, sf)
		}
		switch op.kind {
		case opAdd:
			plan.summaryLines = append(plan.summaryLines, "add "+op.path)
		case opDelete:
			plan.summaryLines = append(plan.summaryLines, "delete "+op.path)
		case opUpdate:
			if op.moveTo != "" && op.moveTo != op.path {
				plan.summaryLines = append(plan.summaryLines, "rename+update "+op.path+" -> "+op.moveTo)
			} else {
				plan.summaryLines = append(plan.summaryLines, "update "+op.path)
			}
		}
	}
	return plan, nil
}

// joinAdd reconstructs the file body for an Add File. The patch
// grammar's "+" prefix on each line is the file's own newline-
// separated text — we join with "\n" and append a trailing newline
// because just about every text file is line-terminated.
func joinAdd(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// applyHunks applies every hunk in order to body, returning the new
// content. Each hunk is located by finding the longest matching
// context-and-removed block, anchored by any leading @@ header lines.
// Lookup starts from the cursor advanced by the previous hunk so
// hunks earlier in the file don't shadow later ones with similar
// context.
func applyHunks(path, body string, hunks []parsedHunk) (string, error) {
	lines := splitLines(body)
	cursor := 0
	for hi, h := range hunks {
		match, err := findHunk(path, lines, cursor, h)
		if err != nil {
			return "", fmt.Errorf("hunk %d of %d: %w", hi+1, len(hunks), err)
		}
		// Build the replacement: context + added lines, in hunk order.
		var replacement []string
		for _, hl := range h.lines {
			if hl.kind == '-' {
				continue
			}
			replacement = append(replacement, hl.text)
		}
		// Splice into lines.
		before := lines[:match.start]
		after := lines[match.end:]
		out := make([]string, 0, len(before)+len(replacement)+len(after))
		out = append(out, before...)
		out = append(out, replacement...)
		out = append(out, after...)
		lines = out
		cursor = match.start + len(replacement)
	}
	return joinLines(lines), nil
}

// hunkMatch records where in the source file's line array the hunk's
// "pre-image" (context + removed) block was found.
type hunkMatch struct {
	start, end int // [start, end) over the lines slice
}

// findHunk locates the pre-image block for h within lines, starting
// no earlier than fromCursor. Anchors are advisory hints used to
// narrow the search window when multiple matches would otherwise be
// ambiguous.
func findHunk(path string, lines []string, fromCursor int, h parsedHunk) (hunkMatch, error) {
	// Pre-image is the sequence of " " and "-" lines.
	var preimg []string
	for _, hl := range h.lines {
		if hl.kind == '+' {
			continue
		}
		preimg = append(preimg, hl.text)
	}
	if len(preimg) == 0 {
		// Pure-addition hunk. Without context we can't pin a location —
		// require at least one context line or an EOF marker.
		if h.endOfFile {
			return hunkMatch{start: len(lines), end: len(lines)}, nil
		}
		return hunkMatch{}, fmt.Errorf(
			"%s: pure-addition hunk needs at least one context line or *** End of File marker",
			path,
		)
	}

	// Apply anchors: find an anchor line at or after fromCursor; start
	// the pre-image search from there. Stacked anchors narrow further.
	searchStart := fromCursor
	for _, anchor := range h.headerAnchors {
		idx := indexOfSubstring(lines, searchStart, anchor)
		if idx < 0 {
			return hunkMatch{}, fmt.Errorf("%s: anchor %q not found after line %d", path, anchor, fromCursor+1)
		}
		searchStart = idx + 1
	}

	// An *** End of File marker pins the hunk to the file's tail. Without this,
	// a hunk whose context also appears earlier (a closing brace, a blank line,
	// repeated boilerplate) would match that FIRST occurrence and silently edit
	// the wrong region. Require the pre-image flush against the end of content.
	if h.endOfFile {
		// A file ending in "\n" splits to a trailing "" element; the logical
		// end of content is one before it, so the pre-image's last line sits at
		// eofPos-1, not len(lines)-1.
		eofPos := len(lines)
		if eofPos > 0 && lines[eofPos-1] == "" {
			eofPos--
		}
		start := eofPos - len(preimg)
		if start < searchStart || start < 0 {
			return hunkMatch{}, fmt.Errorf("%s: end-of-file hunk context not found at the end of the file", path)
		}
		for j := range preimg {
			if lines[start+j] != preimg[j] {
				return hunkMatch{}, fmt.Errorf("%s: end-of-file hunk context does not match the file's tail", path)
			}
		}
		return hunkMatch{start: start, end: eofPos}, nil
	}

	// Scan for the first place the pre-image matches contiguously.
	last := len(lines) - len(preimg)
	for i := searchStart; i <= last; i++ {
		match := true
		for j := range preimg {
			if lines[i+j] != preimg[j] {
				match = false
				break
			}
		}
		if match {
			return hunkMatch{start: i, end: i + len(preimg)}, nil
		}
	}
	// Helpful failure: show the first pre-image line so the model can
	// see what it asked us to match against.
	hint := ""
	if len(preimg) > 0 {
		hint = fmt.Sprintf(" (looking for %q)", preimg[0])
	}
	return hunkMatch{}, fmt.Errorf("%s: hunk context not found in file%s", path, hint)
}

// indexOfSubstring returns the first line index >= from where the line
// contains anchor as a substring. Anchors are matched fuzzily because
// the model often writes `@@ class Foo` against a source line that
// actually reads `class Foo(Bar):` — we want both to align.
func indexOfSubstring(lines []string, from int, anchor string) int {
	for i := from; i < len(lines); i++ {
		if strings.Contains(lines[i], anchor) {
			return i
		}
	}
	return -1
}

// splitLines splits body on "\n". Unlike strings.Split, it preserves
// the "trailing newline = empty final element" distinction so we can
// rebuild it byte-identical in joinLines.
func splitLines(body string) []string {
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// joinLines is the inverse of splitLines.
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

// --- Committer ---

// commitPatch writes every staged file in plan order. Two-phase to
// keep the commit atomic in the success case: validate paths first
// (so a workspace-escape error blocks all writes), then write.
func (t *ApplyPatchTool) commitPatch(plan patchPlan) error {
	// Phase 1: resolve and lock every affected absolute path. Locking
	// here (not per-file inside the loop) means concurrent edits to a
	// file we're about to delete block on the lock until we're done.
	type resolved struct {
		sf     stagedFile
		abs    string
		unlock func()
	}
	rs := make([]resolved, 0, len(plan.files))
	defer func() {
		// Release locks in reverse order; safe to call on panic too.
		for i := len(rs) - 1; i >= 0; i-- {
			if rs[i].unlock != nil {
				rs[i].unlock()
			}
		}
	}()
	for _, sf := range plan.files {
		abs, err := t.ws.Resolve(sf.dstPath)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", sf.dstPath, err)
		}
		rs = append(rs, resolved{sf: sf, abs: abs, unlock: t.ws.LockPath(abs)})
	}
	// Phase 2: apply. All FS operations route through the workspace's
	// [*os.Root] handle so symlinked-parent escape attempts during
	// the apply window are refused at the kernel level.
	for _, r := range rs {
		if r.sf.delete {
			if err := t.ws.RemoveInRoot(r.abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete %q: %w", r.sf.dstPath, err)
			}
			continue
		}
		if err := t.ws.WriteFileInRoot(r.abs, []byte(r.sf.contents), filesystem.ModePublicFile); err != nil {
			return fmt.Errorf("write %q: %w", r.sf.dstPath, err)
		}
	}
	return nil
}
