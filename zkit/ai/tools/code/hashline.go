package code

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
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
)

// ReadFileHLTool reads a file with line-number + hash anchors for the edit
// tool. Its Definition returns ToolNameRead — it replaces ReadTool in the
// standard toolset without changing the name the model sees.
type ReadFileHLTool struct{ ws Workspace }

// ReadFileHLArgs is the typed argument struct ReadFileHLTool.Execute decodes.
type ReadFileHLArgs struct {
	Path    string `json:"path" doc:"Path relative to the workspace root (or absolute, must be inside root)."`
	Offset  int    `json:"offset,omitempty" doc:"Zero-based line offset to start reading from."`
	Limit   int    `json:"limit,omitempty" doc:"Maximum number of lines to return (default 2000)."`
	HashLen int    `json:"hash_len,omitempty" doc:"Hash prefix length: 3 or 4 base64 SHA-256 characters (default 4)."`
}

// NewReadFileHLTool returns the hashline read tool bound to ws.
func NewReadFileHLTool(ws Workspace) *ReadFileHLTool { return &ReadFileHLTool{ws: ws} }

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
	var args ReadFileHLArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("read", "path required")), nil
	}
	hashLen, err := hashlineHashLen(args.HashLen)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("read", err.Error())), nil
	}

	data, fail := readHashlineFile(t.ws, args.Path, call.ID, "read")
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
	Path      string `json:"path" doc:"Path inside the workspace."`
	StartLine int    `json:"start_line" doc:"1-based line anchor from the read output."`
	StartHash string `json:"start_hash" doc:"3- or 4-character base64 SHA-256 hash for start_line from the read output."`
	EndLine   int    `json:"end_line,omitempty" doc:"Inclusive 1-based end line for replace/delete. Omit for a single-line edit."`
	EndHash   string `json:"end_hash,omitempty" doc:"3- or 4-character base64 SHA-256 hash for end_line from the read output."`
	NewString string `json:"new_string,omitempty" doc:"Replacement or insertion bytes. Include newline characters exactly as desired."`
	Mode      string `json:"mode,omitempty" enum:"replace,delete,insert_before,insert_after" doc:"Edit mode: replace (default), delete, insert_before, or insert_after."`
}

// NewEditFileHLTool returns the hashline edit tool bound to ws.
func NewEditFileHLTool(ws Workspace) *EditFileHLTool { return &EditFileHLTool{ws: ws} }

// Definition advertises edit as a mutating line-anchor edit tool.
func (t *EditFileHLTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameEdit,
		Description: "Edit a workspace file using line/hash anchors from the read output. " +
			"Replaces or deletes an anchored line range, or inserts before/after an anchored line. " +
			"The hash identifies the line by content, so anchors survive line-number shifts from earlier edits — you can apply several edits from one read. " +
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
	var args EditFileHLArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("edit", "path required")), nil
	}
	if maxEditArgBytes > 0 && len(args.NewString) > maxEditArgBytes {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf(
			"%q: new_string too large (%d bytes; cap %d). Split into smaller edits.",
			args.Path, len(args.NewString), maxEditArgBytes))), nil
	}
	mode := args.Mode
	if mode == "" {
		mode = hashlineModeReplace
	}
	if err := validateHashlineEditArgs(args, mode); err != nil {
		return tools.Failure(call.ID, tools.Validation("edit", err.Error())), nil
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("edit", err.Error())), nil
	}
	unlock := t.ws.LockPath(abs)
	defer unlock()

	data, fail := readHashlineFileAt(t.ws, abs, args.Path, call.ID, "edit")
	if fail != nil {
		return fail, nil
	}
	body := string(data)
	lines := hashlineLines(body)

	start, err := verifyHashlineAnchor(args.Path, "start", lines, args.StartLine, args.StartHash)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}

	spliceStart := start.Start
	spliceEnd := start.End
	replacement := args.NewString
	summary := fmt.Sprintf("%s line %d", mode, args.StartLine)

	switch mode {
	case hashlineModeReplace, hashlineModeDelete:
		endLine := args.EndLine
		endHash := args.EndHash
		if endLine == 0 && endHash == "" {
			endLine = args.StartLine
			endHash = args.StartHash
		}
		end, err := verifyHashlineAnchor(args.Path, "end", lines, endLine, endHash)
		if err != nil {
			return tools.Failure(call.ID, err), nil
		}
		if endLine < args.StartLine {
			return tools.Failure(call.ID, tools.Validation(
				"edit",
				fmt.Sprintf("%q: end_line %d is before start_line %d", args.Path, endLine, args.StartLine),
			)), nil
		}
		spliceEnd = end.End
		if spliceEnd < spliceStart {
			return tools.Failure(call.ID, tools.Stale(
				"edit",
				fmt.Sprintf("%q: resolved end anchor precedes start anchor after the file shifted; re-run read on this file and retry with fresh anchors", args.Path),
			)), nil
		}
		if mode == hashlineModeDelete {
			replacement = ""
		}
		if endLine != args.StartLine {
			summary = fmt.Sprintf("%s lines %d-%d", mode, args.StartLine, endLine)
		}
	case hashlineModeInsertBefore:
		spliceEnd = spliceStart
	case hashlineModeInsertAfter:
		spliceStart = start.End
		spliceEnd = start.End
	}

	updated := body[:spliceStart] + replacement + body[spliceEnd:]
	if err := t.ws.WriteFileInRoot(abs, []byte(updated), filesystem.ModePublicFile); err != nil {
		return tools.Failure(call.ID, tools.Fatal("edit", fmt.Errorf("write %q: %w", args.Path, err))), nil
	}
	effect := tools.NewFileEffect(tools.FileModify, args.Path)
	effect.File.BytesAfter = int64(len(updated))
	return tools.Success(call.ID, fmt.Sprintf("edited %s (%s, hashline anchors verified)", args.Path, summary), effect), nil
}

type hashlineLine struct {
	Start   int
	End     int
	Content string
}

func readHashlineFile(ws Workspace, path, callID, op string) ([]byte, *tools.ToolResult) {
	abs, err := ws.Resolve(path)
	if err != nil {
		return nil, tools.Failure(callID, tools.Permission(op, err.Error()))
	}
	return readHashlineFileAt(ws, abs, path, callID, op)
}

func readHashlineFileAt(ws Workspace, abs, path, callID, op string) ([]byte, *tools.ToolResult) {
	info, err := ws.StatInRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tools.Failure(callID, tools.NotFound(op, fmt.Sprintf("%q does not exist", path)))
		}
		return nil, tools.Failure(callID, tools.Fatal(op, fmt.Errorf("stat %q: %w", path, err)))
	}
	if info.Size() > readMaxBytes {
		return nil, tools.Failure(callID, tools.Validation(
			op,
			fmt.Sprintf("%q: file too large (%d bytes, max %d)", path, info.Size(), readMaxBytes),
		))
	}

	data, err := ws.ReadFileInRoot(abs)
	if err != nil {
		return nil, tools.Failure(callID, tools.Fatal(op, fmt.Errorf("%q: %w", path, err)))
	}
	sniffEnd := min(len(data), binarySniffBytes)
	if bytes.IndexByte(data[:sniffEnd], 0) >= 0 {
		return nil, tools.Failure(callID, tools.Validation(op, fmt.Sprintf("%q: binary file refused", path)))
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
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
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

func validateHashlineEditArgs(args EditFileHLArgs, mode string) error {
	if args.StartLine <= 0 {
		return fmt.Errorf("%q: start_line must be positive", args.Path)
	}
	if err := validateHashlineToken(args.StartHash, "start_hash"); err != nil {
		return err
	}
	switch mode {
	case hashlineModeReplace:
		return validateHashlineRange(args)
	case hashlineModeDelete:
		if args.NewString != "" {
			return fmt.Errorf("%q: delete mode requires empty new_string", args.Path)
		}
		return validateHashlineRange(args)
	case hashlineModeInsertBefore, hashlineModeInsertAfter:
		if args.NewString == "" {
			return fmt.Errorf("%q: %s mode requires non-empty new_string", args.Path, mode)
		}
		if args.EndLine != 0 || args.EndHash != "" {
			return fmt.Errorf("%q: end_line/end_hash are only valid for replace or delete", args.Path)
		}
		return nil
	default:
		return fmt.Errorf("%q: mode must be one of replace, delete, insert_before, insert_after", args.Path)
	}
}

func validateHashlineRange(args EditFileHLArgs) error {
	if args.EndLine == 0 && args.EndHash == "" {
		return nil
	}
	if args.EndLine <= 0 {
		return fmt.Errorf("%q: end_line must be positive when end_hash is set", args.Path)
	}
	if args.EndHash == "" {
		return fmt.Errorf("%q: end_hash required when end_line is set", args.Path)
	}
	return validateHashlineToken(args.EndHash, "end_hash")
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
