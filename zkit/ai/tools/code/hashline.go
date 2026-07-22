package code

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

const (
	hashlineDefaultHashLen = 4
	hashlineMinHashLen     = 3
	hashlineMaxHashLen     = 4

	hashlineModeReplace      = "replace"
	hashlineModeDelete       = "delete"
	hashlineModeInsertBefore = "insert_before"
	hashlineModeInsertAfter  = "insert_after"

	hashlineMaxBatchEdits = 64

	// hashlineEditWindowContext is the number of unchanged lines shown on
	// each side of a spliced region in the post-edit anchor window.
	hashlineEditWindowContext = 3
	// hashlineEditWindowMaxLines caps the total lines emitted across all
	// windows so a large batch edit can't reflate the result back into a
	// full-file read — past the cap the model is better off re-reading.
	hashlineEditWindowMaxLines = 80
)

// ReadFileHLTool reads a file with line-number + hash anchors for the edit
// tool. Its Definition returns ToolNameRead — it replaces ReadTool in the
// standard toolset without changing the name the model sees.
type ReadFileHLTool struct {
	ws                    Workspace
	allowOutsideWorkspace bool
}

// ReadFileHLArgs is the typed argument struct ReadFileHLTool.Execute decodes.
type ReadFileHLArgs struct {
	Path    string `json:"path" doc:"Path relative to the workspace root (or absolute, must be inside root)."`
	Offset  int    `json:"offset,omitempty" doc:"Zero-based line offset to start reading from."`
	Limit   int    `json:"limit,omitempty" doc:"Maximum number of lines to return (default 2000)."`
	HashLen int    `json:"hash_len,omitempty" doc:"Hash prefix length: 3 or 4 base64 SHA-256 characters (default 4)."`
}

// NewReadFileHLTool returns the hashline read tool bound to ws.
func NewReadFileHLTool(ws Workspace, opts ...ReadOption) *ReadFileHLTool {
	var policy readPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return &ReadFileHLTool{ws: ws, allowOutsideWorkspace: policy.allowOutsideWorkspace}
}

// Definition advertises read with path, offset, limit, and hash_len.
func (t *ReadFileHLTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameRead,
		Description: "Read a text file with stable line anchors for anchored edits. " +
			"Each row is LINE:HASH|text, where HASH is a 3/4-character base64 SHA-256 prefix of the displayed line content.",
		Parameters: tools.SchemaFor[ReadFileHLArgs](),
	}
}

// Execute returns line-oriented file content as LINE:HASH|text rows. Hashes
// are computed over displayed line content only; LF and CRLF terminators are
// not included, while other whitespace is.
func (t *ReadFileHLTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[ReadFileHLArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("read", "path required")), nil
	}
	hashLen, err := hashlineHashLen(args.HashLen)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("read", err.Error())), nil
	}

	data, fail := readHashlineFile(t.ws, args.Path, call.ID.String(), "read", t.allowOutsideWorkspace)
	if fail != nil {
		return fail, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = readDefaultLimit
	}
	if args.Offset < 0 {
		args.Offset = 0
	}

	lines := hashlineLines(string(data))
	if args.Offset >= len(lines) {
		return tools.Success(call.ID, ""), nil
	}
	wantedEnd := args.Offset + limit
	end := min(wantedEnd, len(lines))
	truncated := wantedEnd < len(lines)

	var b strings.Builder
	for i := args.Offset; i < end; i++ {
		line := lines[i]
		fmt.Fprintf(&b, "%d:%s|%s\n", i+1, hashlineHash(line.Content, hashLen), line.Content)
	}
	if truncated {
		fmt.Fprintf(&b, "... (truncated at line %d of %d)\n", end, len(lines))
	}
	return tools.Success(call.ID, b.String()), nil
}

// EditFileHLTool edits a workspace file through the read output's line/hash
// anchors. Its Definition returns ToolNameEdit — it replaces EditTool in
// the standard toolset without changing the name the model sees.
type EditFileHLTool struct{ ws Workspace }

// EditFileHLArgs is the typed argument struct EditFileHLTool.Execute decodes.
type EditFileHLArgs struct {
	Path string `json:"path" doc:"Path inside the workspace."`

	Edits []HashlineEdit `json:"edits" doc:"One or more edits to apply atomically to this file. Use a one-item array for a single edit; all anchors are verified before writing."`
}

// HashlineEdit describes one edit inside EditFileHLArgs.Edits.
type HashlineEdit struct {
	StartLine int    `json:"start_line" doc:"1-based line anchor from the read output."`
	StartHash string `json:"start_hash" doc:"3- or 4-character base64 SHA-256 hash for start_line from the read output."`
	EndLine   int    `json:"end_line,omitempty" doc:"Inclusive 1-based end line for replace/delete. Omit for a single-line edit."`
	EndHash   string `json:"end_hash,omitempty" doc:"3- or 4-character base64 SHA-256 hash for end_line from the read output."`
	NewString string `json:"new_string,omitempty" doc:"Replacement or insertion bytes. Include newline characters exactly as desired; a replace whose line already ended in a newline stays newline-terminated even if you omit the trailing newline. Use a single cohesive range edit when changing adjacent lines."`
	Mode      string `json:"mode,omitempty" enum:"replace,delete,insert_before,insert_after" doc:"Edit mode: replace (default), delete, insert_before, or insert_after."`
}

// NewEditFileHLTool returns the hashline edit tool bound to ws.
func NewEditFileHLTool(ws Workspace) *EditFileHLTool { return &EditFileHLTool{ws: ws} }

// Definition advertises edit as a mutating line-anchor edit tool.
func (t *EditFileHLTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameEdit,
		Description: "Edit a workspace file using line/hash anchors from the read output. " +
			"Always pass edits as an array, even for a single edit. " +
			"For several changes in one file, use one call so the edits apply atomically. " +
			"Prefer one well-scoped range edit for a cohesive change instead of many tiny adjacent edits. " +
			"Replaces or deletes anchored line ranges, or inserts before/after anchored lines. " +
			"The hash identifies the line by content, so anchors survive line-number shifts from earlier edits. " +
			"On success the result returns fresh line/hash anchors around the edited region — use those to make further edits to the same file without re-reading it. " +
			"A stale error means the content changed; re-read that file and retry with fresh anchors.",
		Parameters: tools.SchemaFor[EditFileHLArgs](),
		Mutates:    true,
	}
}

// Execute verifies the requested line/hash anchors against the current file
// and then performs one byte-level splice at line boundaries. The old file
// content never has to be reproduced in the arguments; stale anchors are
// refused before any write occurs.
func (t *EditFileHLTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[EditFileHLArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("edit", "path required")), nil
	}
	edits, err := normalizeHashlineEdits(args)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("edit", err.Error())), nil
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("edit", err.Error())), nil
	}
	unlock := t.ws.LockPath(abs)
	defer unlock()

	data, fail := readHashlineFileAt(t.ws, abs, args.Path, call.ID.String(), "edit")
	if fail != nil {
		return fail, nil
	}
	body := string(data)
	lines := hashlineLines(body)

	resolved := make([]resolvedHashlineEdit, 0, len(edits))
	for i, edit := range edits {
		item, err := resolveHashlineEdit(args.Path, lines, i, edit)
		if err != nil {
			return tools.Failure(call.ID, err), nil
		}
		resolved = append(resolved, item)
	}
	if err := validateResolvedHashlineEdits(args.Path, resolved); err != nil {
		return tools.Failure(call.ID, tools.Validation("edit", err.Error())), nil
	}

	preserveLineTerminators(body, resolved)
	updated := applyResolvedHashlineEdits(body, resolved)
	if err := t.ws.WriteFileInRoot(abs, []byte(updated), filesystem.ModePublicFile); err != nil {
		return tools.Failure(call.ID, tools.Fatal("edit", fmt.Errorf("write %q: %w", args.Path, err))), nil
	}
	effect := tools.NewFileEffect(tools.FileModify, args.Path)
	effect.File.BytesAfter = int64(len(updated))
	msg := fmt.Sprintf("edited %s (%s, hashline anchors verified)", args.Path, hashlineEditSummary(resolved))
	// Emit fresh LINE:HASH|text anchors around each spliced region so the
	// model can chain further edits to this file without a re-read — the
	// line numbers and hashes below every splice have just shifted, which is
	// what otherwise forces the read→edit→read→edit loop.
	if window := hashlineEditWindows(updated, resolved); window != "" {
		msg += "\n\nfresh anchors after this edit (continue editing without re-reading):\n" + window
	}
	return tools.Success(call.ID, msg, effect), nil
}

type resolvedHashlineEdit struct {
	Index       int
	Mode        string
	StartLine   int
	EndLine     int
	SpliceStart int
	SpliceEnd   int
	Replacement string
	Summary     string
}

func normalizeHashlineEdits(args EditFileHLArgs) ([]HashlineEdit, error) {
	if len(args.Edits) > hashlineMaxBatchEdits {
		return nil, fmt.Errorf("%q: edits has %d items; max %d", args.Path, len(args.Edits), hashlineMaxBatchEdits)
	}
	if err := validateHashlineEditList(args.Path, args.Edits); err != nil {
		return nil, err
	}
	return args.Edits, nil
}

func validateHashlineEditList(path string, edits []HashlineEdit) error {
	if len(edits) == 0 {
		return fmt.Errorf("%q: at least one edit is required", path)
	}
	totalNewBytes := 0
	for i, edit := range edits {
		mode := hashlineEditMode(edit)
		if err := validateHashlineEdit(path, i, edit, mode); err != nil {
			return err
		}
		totalNewBytes += len(edit.NewString)
	}
	if maxEditArgBytes > 0 && totalNewBytes > maxEditArgBytes {
		return fmt.Errorf("%q: total new_string too large (%d bytes; cap %d)", path, totalNewBytes, maxEditArgBytes)
	}
	return nil
}

func validateHashlineEdit(path string, index int, edit HashlineEdit, mode string) error {
	prefix := hashlineEditErrorPrefix(path, index)
	if edit.StartLine <= 0 {
		return fmt.Errorf("%s: start_line must be positive", prefix)
	}
	if err := validateHashlineToken(edit.StartHash, "start_hash"); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if maxEditArgBytes > 0 && len(edit.NewString) > maxEditArgBytes {
		return fmt.Errorf("%s: new_string too large (%d bytes; cap %d)", prefix, len(edit.NewString), maxEditArgBytes)
	}
	switch mode {
	case hashlineModeReplace:
		return validateHashlineEditRange(prefix, edit)
	case hashlineModeDelete:
		if edit.NewString != "" {
			return fmt.Errorf("%s: delete mode requires empty new_string", prefix)
		}
		return validateHashlineEditRange(prefix, edit)
	case hashlineModeInsertBefore, hashlineModeInsertAfter:
		if edit.NewString == "" {
			return fmt.Errorf("%s: %s mode requires non-empty new_string", prefix, mode)
		}
		if err := validateHashlineInsertExtraRange(prefix, edit); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%s: mode must be one of replace, delete, insert_before, insert_after", prefix)
	}
}

func validateHashlineEditRange(prefix string, edit HashlineEdit) error {
	if edit.EndLine == 0 && edit.EndHash == "" {
		return nil
	}
	if edit.EndLine <= 0 {
		return fmt.Errorf("%s: end_line must be positive when end_hash is set", prefix)
	}
	if edit.EndHash == "" {
		return fmt.Errorf("%s: end_hash required when end_line is set", prefix)
	}
	if err := validateHashlineToken(edit.EndHash, "end_hash"); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return nil
}

func validateHashlineInsertExtraRange(prefix string, edit HashlineEdit) error {
	if edit.EndLine == 0 && edit.EndHash == "" {
		return nil
	}
	if edit.EndLine == edit.StartLine && edit.EndHash == edit.StartHash {
		return nil
	}
	return fmt.Errorf("%s: end_line/end_hash are ignored for insert_before/insert_after; omit them or make them match start_line/start_hash", prefix)
}

func resolveHashlineEdit(path string, lines []hashlineLine, index int, edit HashlineEdit) (resolvedHashlineEdit, error) {
	mode := hashlineEditMode(edit)
	start, err := verifyHashlineAnchor(path, "start", lines, edit.StartLine, edit.StartHash)
	if err != nil {
		return resolvedHashlineEdit{}, err
	}

	resolved := resolvedHashlineEdit{
		Index:       index,
		Mode:        mode,
		StartLine:   edit.StartLine,
		EndLine:     edit.StartLine,
		SpliceStart: start.Start,
		SpliceEnd:   start.End,
		Replacement: edit.NewString,
		Summary:     fmt.Sprintf("%s line %d", mode, edit.StartLine),
	}

	switch mode {
	case hashlineModeReplace, hashlineModeDelete:
		endLine := edit.EndLine
		endHash := edit.EndHash
		if endLine == 0 && endHash == "" {
			endLine = edit.StartLine
			endHash = edit.StartHash
		}
		end, err := verifyHashlineAnchor(path, "end", lines, endLine, endHash)
		if err != nil {
			return resolvedHashlineEdit{}, err
		}
		if endLine < edit.StartLine {
			return resolvedHashlineEdit{}, tools.Validation(
				"edit",
				fmt.Sprintf("%s: end_line %d is before start_line %d", hashlineEditErrorPrefix(path, index), endLine, edit.StartLine),
			)
		}
		resolved.EndLine = endLine
		resolved.SpliceEnd = end.End
		if resolved.SpliceEnd < resolved.SpliceStart {
			return resolvedHashlineEdit{}, tools.Stale(
				"edit",
				fmt.Sprintf("%q: resolved end anchor precedes start anchor after the file shifted; re-run read on this file and retry with fresh anchors", path),
			)
		}
		if mode == hashlineModeDelete {
			resolved.Replacement = ""
		}
		if endLine != edit.StartLine {
			resolved.Summary = fmt.Sprintf("%s lines %d-%d", mode, edit.StartLine, endLine)
		}
	case hashlineModeInsertBefore:
		resolved.SpliceEnd = resolved.SpliceStart
	case hashlineModeInsertAfter:
		resolved.SpliceStart = start.End
		resolved.SpliceEnd = start.End
	}
	return resolved, nil
}

func validateResolvedHashlineEdits(path string, edits []resolvedHashlineEdit) error {
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b resolvedHashlineEdit) int {
		if a.SpliceStart != b.SpliceStart {
			return a.SpliceStart - b.SpliceStart
		}
		if a.SpliceEnd != b.SpliceEnd {
			return a.SpliceEnd - b.SpliceEnd
		}
		return a.Index - b.Index
	})
	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1]
		cur := sorted[i]
		if prev.SpliceStart == prev.SpliceEnd && cur.SpliceStart == cur.SpliceEnd && prev.SpliceStart == cur.SpliceStart {
			continue
		}
		if cur.SpliceStart < prev.SpliceEnd {
			return fmt.Errorf("%q: edits %d and %d overlap", path, prev.Index+1, cur.Index+1)
		}
	}
	return nil
}

// preserveLineTerminators keeps a replaced line range line-terminated. The edit
// tool anchors on whole lines, so a replace whose spliced-out range ended in a
// newline should stay newline-terminated. Models routinely omit the trailing
// newline in new_string (and some providers drop a trailing newline while
// streaming the argument), which would silently un-terminate the line — merging
// it with the next one, or dropping the file's final newline. When the removed
// range ended in "\n" but the replacement doesn't, re-append it (matching the
// original CRLF/LF terminator). Deletes (empty replacement) and inserts
// (zero-width splice) are unaffected by construction.
func preserveLineTerminators(body string, edits []resolvedHashlineEdit) {
	for i := range edits {
		e := &edits[i]
		if e.SpliceEnd <= e.SpliceStart || e.Replacement == "" {
			continue
		}
		if body[e.SpliceEnd-1] != '\n' || strings.HasSuffix(e.Replacement, "\n") {
			continue
		}
		if e.SpliceEnd >= 2 && body[e.SpliceEnd-2] == '\r' {
			e.Replacement += "\r\n"
			continue
		}
		e.Replacement += "\n"
	}
}

func applyResolvedHashlineEdits(body string, edits []resolvedHashlineEdit) string {
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b resolvedHashlineEdit) int {
		if a.SpliceStart != b.SpliceStart {
			return b.SpliceStart - a.SpliceStart
		}
		return b.Index - a.Index
	})
	updated := body
	for _, edit := range sorted {
		updated = updated[:edit.SpliceStart] + edit.Replacement + updated[edit.SpliceEnd:]
	}
	return updated
}

// hashlineEditWindows renders fresh LINE:HASH|text anchors around every
// spliced region of the post-edit body, so the model can keep editing the
// same file without re-reading it. Line numbers and hashes are recomputed on
// updated (the bytes just written); windows that overlap or sit within one
// line of each other merge, and the whole block is dropped when it would
// exceed hashlineEditWindowMaxLines — past that a re-read is cheaper than
// bloating every edit result. Returns "" when there is nothing useful to show.
func hashlineEditWindows(updated string, edits []resolvedHashlineEdit) string {
	lines := hashlineLines(updated)
	if len(lines) == 0 {
		return ""
	}

	// New byte position of each replacement, ascending by original splice,
	// accumulating the length delta of all earlier edits. This mirrors
	// applyResolvedHashlineEdits, which splices from the tail so an earlier
	// edit's prefix is never disturbed by a later one.
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b resolvedHashlineEdit) int {
		if a.SpliceStart != b.SpliceStart {
			return a.SpliceStart - b.SpliceStart
		}
		return a.Index - b.Index
	})

	type span struct{ lo, hi int } // inclusive line indices
	spans := make([]span, 0, len(sorted))
	delta := 0
	for _, e := range sorted {
		newStart := e.SpliceStart + delta
		newEnd := newStart + len(e.Replacement) // exclusive
		delta += len(e.Replacement) - (e.SpliceEnd - e.SpliceStart)

		lo := hashlineLineIndexAt(lines, newStart)
		hiByte := newEnd - 1
		if hiByte < newStart {
			// Zero-length replacement (delete): anchor on the splice line so
			// the model sees where the removed range used to be.
			hiByte = newStart
		}
		hi := hashlineLineIndexAt(lines, hiByte)

		lo = max(lo-hashlineEditWindowContext, 0)
		hi = min(hi+hashlineEditWindowContext, len(lines)-1)
		spans = append(spans, span{lo, hi})
	}

	// Merge overlapping or single-line-adjacent spans; spans are already in
	// ascending order because sorted is.
	merged := make([]span, 0, len(spans))
	for _, s := range spans {
		if n := len(merged); n > 0 && s.lo <= merged[n-1].hi+1 {
			merged[n-1].hi = max(merged[n-1].hi, s.hi)
			continue
		}
		merged = append(merged, s)
	}

	total := 0
	for _, s := range merged {
		total += s.hi - s.lo + 1
	}
	if total > hashlineEditWindowMaxLines {
		return ""
	}

	var b strings.Builder
	for i, s := range merged {
		if i > 0 {
			fmt.Fprintf(&b, "... (%d unchanged)\n", s.lo-merged[i-1].hi-1)
		}
		for j := s.lo; j <= s.hi; j++ {
			line := lines[j]
			fmt.Fprintf(&b, "%d:%s|%s\n", j+1, hashlineHash(line.Content, hashlineDefaultHashLen), line.Content)
		}
	}
	return b.String()
}

// hashlineLineIndexAt returns the index of the line whose [Start,End) byte
// range contains off. Lines are contiguous and ascending, so a binary search
// on End finds it; an offset at end-of-file clamps to the last line.
func hashlineLineIndexAt(lines []hashlineLine, off int) int {
	i := sort.Search(len(lines), func(i int) bool { return lines[i].End > off })
	if i >= len(lines) {
		return len(lines) - 1
	}
	return i
}

func hashlineEditMode(edit HashlineEdit) string {
	if edit.Mode == "" {
		return hashlineModeReplace
	}
	return edit.Mode
}

func hashlineEditSummary(edits []resolvedHashlineEdit) string {
	if len(edits) == 1 {
		return edits[0].Summary
	}
	parts := make([]string, len(edits))
	for i, edit := range edits {
		parts[i] = edit.Summary
	}
	return fmt.Sprintf("%d edits: %s", len(edits), strings.Join(parts, "; "))
}

func hashlineEditErrorPrefix(path string, index int) string {
	if index < 0 {
		return fmt.Sprintf("%q", path)
	}
	return fmt.Sprintf("%q edit %d", path, index+1)
}

type hashlineLine struct {
	Start   int
	End     int
	Content string
}

func readHashlineFile(ws Workspace, path, callID, op string, allowOutsideWorkspace bool) ([]byte, *tools.ToolResult) {
	abs, err := ws.ResolveForRead(path, allowOutsideWorkspace)
	if err != nil {
		return nil, tools.Failure(tools.ToolCallID(callID), tools.Permission(op, err.Error()))
	}
	return readHashlineFileAt(ws, abs, path, callID, op)
}

func readHashlineFileAt(ws Workspace, abs, path, callID, op string) ([]byte, *tools.ToolResult) {
	info, err := ws.StatPath(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tools.Failure(tools.ToolCallID(callID), tools.NotFound(op, fmt.Sprintf("%q does not exist", path)))
		}
		return nil, tools.Failure(tools.ToolCallID(callID), tools.Fatal(op, fmt.Errorf("stat %q: %w", path, err)))
	}
	if info.Size() > readMaxBytes {
		return nil, tools.Failure(tools.ToolCallID(callID), tools.Validation(
			op,
			fmt.Sprintf("%q: file too large (%d bytes, max %d)", path, info.Size(), readMaxBytes),
		))
	}

	data, err := ws.ReadFilePath(abs)
	if err != nil {
		return nil, tools.Failure(tools.ToolCallID(callID), tools.Fatal(op, fmt.Errorf("%q: %w", path, err)))
	}
	sniffEnd := min(len(data), binarySniffBytes)
	if bytes.IndexByte(data[:sniffEnd], 0) >= 0 {
		return nil, tools.Failure(tools.ToolCallID(callID), tools.Validation(op, fmt.Sprintf("%q: binary file refused", path)))
	}
	return data, nil
}

func hashlineLines(body string) []hashlineLine {
	parts := splitKeepingEOL(body)
	lines := make([]hashlineLine, 0, len(parts))
	pos := 0
	for _, part := range parts {
		start := pos
		pos += len(part)
		lines = append(lines, hashlineLine{Start: start, End: pos, Content: hashlineLineContent(part)})
	}
	return lines
}

func hashlineLineContent(line string) string {
	if before, ok := strings.CutSuffix(line, "\n"); ok {
		line = before
		line = strings.TrimSuffix(line, "\r")
	}
	return line
}

func hashlineHash(content string, length int) string {
	sum := sha256.Sum256([]byte(content))
	return base64.RawStdEncoding.EncodeToString(sum[:])[:length]
}

func hashlineHashLen(length int) (int, error) {
	if length == 0 {
		return hashlineDefaultHashLen, nil
	}
	if length == hashlineMinHashLen || length == hashlineMaxHashLen {
		return length, nil
	}
	return 0, fmt.Errorf("hash_len must be 3 or 4, got %d", length)
}

func validateHashlineToken(hash, field string) error {
	if len(hash) != hashlineMinHashLen && len(hash) != hashlineMaxHashLen {
		return fmt.Errorf("%s must be 3 or 4 base64 SHA-256 characters", field)
	}
	for _, r := range hash {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' {
			continue
		}
		return fmt.Errorf("%s contains non-base64 character %q", field, r)
	}
	return nil
}

// verifyHashlineAnchor resolves an anchor to its current line. The hash is
// computed over content, so it identifies the line independently of position:
// when an earlier edit in the same batch shifts the line number, the fast
// path misses but a hash scan recovers the intended line. lineNo is the
// model's positional intent, used only to break ties between duplicate-content
// lines. A scan that finds nothing means the content itself changed — a stale
// anchor (re-read needed), reported as Kinds.STALE rather than a malformed-
// input VALIDATION so guardrails advise re-reading instead of "fix the format".
func verifyHashlineAnchor(path, label string, lines []hashlineLine, lineNo int, hash string) (hashlineLine, error) {
	if lineNo >= 1 && lineNo <= len(lines) {
		line := lines[lineNo-1]
		if hashlineHash(line.Content, len(hash)) == hash {
			return line, nil
		}
	}
	matches := matchHashlineByHash(lines, hash)
	switch len(matches) {
	case 1:
		return lines[matches[0]], nil
	case 0:
		return hashlineLine{}, tools.Stale(
			"edit",
			fmt.Sprintf(
				"%q: %s_line %d no longer matches hash %s — the file changed since it was read; re-run read on this file and retry with fresh anchors",
				path, label, lineNo, hash,
			),
		)
	default:
		idx, ok := nearestHashlineMatch(matches, lineNo)
		if !ok {
			return hashlineLine{}, tools.Stale(
				"edit",
				fmt.Sprintf(
					"%q: %s_line %d hash %s matches %d lines equally far from the anchor; re-run read on this file and retry with fresh anchors",
					path, label, lineNo, hash, len(matches),
				),
			)
		}
		return lines[idx], nil
	}
}

// matchHashlineByHash returns the zero-based indices of every line whose
// hash (at the anchor's hash length) equals hash.
func matchHashlineByHash(lines []hashlineLine, hash string) []int {
	var idxs []int
	for i, line := range lines {
		if hashlineHash(line.Content, len(hash)) == hash {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// nearestHashlineMatch picks the candidate line index closest to the
// anchor's line number. It reports ok=false when two candidates are
// equally near — duplicate content with a drifted anchor is genuinely
// ambiguous and must be resolved by a fresh read, not a guess.
func nearestHashlineMatch(matches []int, lineNo int) (int, bool) {
	best, bestDist, tie := matches[0], 0, false
	for i, idx := range matches {
		dist := (idx + 1) - lineNo
		if dist < 0 {
			dist = -dist
		}
		if i == 0 || dist < bestDist {
			best, bestDist, tie = idx, dist, false
			continue
		}
		if dist == bestDist {
			tie = true
		}
	}
	return best, !tie
}
