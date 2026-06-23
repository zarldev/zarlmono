package code

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// EditTool performs an exact-string replacement in a workspace file. It is
// retained for legacy consumers and focused tests; the standard coderunner /
// zarlcode tool surface now exposes EditFileHLTool under the same `edit` name.
//
// Without replace_all, old_string must appear exactly once — otherwise the
// edit is rejected to prevent accidental whole-file rewrites.
//
// # Whitespace-tolerant fallback
//
// When exact match returns zero hits (and replace_all is off), the tool
// re-tries on a line-normalised view: per-line trailing whitespace and
// the \r in CRLF are stripped from both old_string and the file before
// the search. If exactly one match exists in the normalised view, the
// corresponding byte range in the *original* file is replaced. The
// model's exact whitespace inside lines and around the splice point
// is preserved; the model is informed in the result message so it
// can see the fuzzy path fired.
//
// Ambiguity is still refused: if normalisation produces multiple hits,
// the edit is rejected with the count so the model adds more context.
//
// # Argument size cap
//
// Both old_string and new_string are capped at maxEditArgBytes
// (default 64KB, tunable via CODE_EDIT_MAX_BYTES — see limits.go).
// The cap is generous — modern providers handle 64KB string args
// cleanly. Historical context: older llama.cpp builds dropped
// characters inside multi-KB streaming tool-call JSON, which is why
// this cap exists at all. The cap is tighter than write's because
// edit always carries two such args (old_string + new_string) and
// the failure mode is per-arg.
type EditTool struct{ ws Workspace }

// EditArgs is the typed argument struct EditTool.Execute decodes
// into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type EditArgs struct {
	Path       string `json:"path" doc:"Path inside the workspace."`
	OldString  string `json:"old_string" doc:"Exact text to replace."`
	NewString  string `json:"new_string" doc:"Replacement text."`
	ReplaceAll bool   `json:"replace_all,omitempty" doc:"Replace every occurrence (default false)."`
}

// NewEditTool returns the legacy exact-string edit tool bound to ws.
func NewEditTool(ws Workspace) *EditTool { return &EditTool{ws: ws} }

// Definition advertises the legacy exact-string edit shape: path,
// old_string, new_string, and replace_all. Mutates is true because a
// successful edit rewrites the file in place.
func (t *EditTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameEdit,
		Description: "Replace exact text in a workspace file. By default old_string must occur exactly once. Set replace_all to substitute every occurrence.",
		Parameters:  tools.SchemaFor[EditArgs](),
		Mutates:     true,
	}
}

// Execute caps old_string and new_string at maxEditArgBytes (64KB
// default) up front, then — holding the path lock across the
// read-modify-write — requires a unique exact match unless replace_all
// is set, falling back to a single-hit whitespace-normalised match
// when the exact search finds nothing. Ambiguity at either stage is
// refused with the match count; success emits a FileModify effect.
//
// This legacy exact-string path remains useful for narrow consumers, but
// the standard coding surface prefers EditFileHLTool's anchored workflow.
func (t *EditTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args EditArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("edit", "path required")), nil
	}
	if args.OldString == "" {
		return tools.Failure(
			call.ID,
			tools.Validation("edit", fmt.Sprintf("%q: old_string must not be empty", args.Path)),
		), nil
	}
	// Reject oversized arguments before touching the filesystem. See
	// EditTool's doc comment for the historical context behind the
	// cap. Set CODE_EDIT_MAX_BYTES at startup to raise the cap if
	// your model+server tolerates even larger streaming args.
	if maxEditArgBytes > 0 && len(args.OldString) > maxEditArgBytes {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf(
			"%q: old_string too large (%d bytes; cap %d). "+
				"Split into smaller edits each replacing under %d bytes — "+
				"identify a unique anchor near each change and edit one section at a time.",
			args.Path, len(args.OldString), maxEditArgBytes, maxEditArgBytes))), nil
	}
	if maxEditArgBytes > 0 && len(args.NewString) > maxEditArgBytes {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf(
			"%q: new_string too large (%d bytes; cap %d). "+
				"Split into smaller edits each writing under %d bytes.",
			args.Path, len(args.NewString), maxEditArgBytes, maxEditArgBytes))), nil
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("edit", err.Error())), nil
	}
	// Hold the path lock across the read-modify-write so a concurrent
	// editor or writer can't race the body we're operating on.
	unlock := t.ws.LockPath(abs)
	defer unlock()

	data, err := t.ws.ReadFileInRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Failure(call.ID, tools.NotFound("edit", fmt.Sprintf("%q does not exist", args.Path))), nil
		}
		return tools.Failure(call.ID, tools.Fatal("edit", fmt.Errorf("read %q: %w", args.Path, err))), nil
	}
	body := string(data)

	count := strings.Count(body, args.OldString)
	if count > 1 && !args.ReplaceAll {
		return tools.Failure(
			call.ID,
			tools.Validation(
				"edit",
				fmt.Sprintf(
					"%q: old_string matches %d times — provide more context or set replace_all",
					args.Path,
					count,
				),
			),
		), nil
	}

	if count == 0 {
		// Fuzzy fallback only when replace_all is off — multi-hit fuzzy
		// replace is too risky to do silently.
		if args.ReplaceAll {
			return tools.Failure(
				call.ID,
				tools.NotFound(
					"edit",
					fmt.Sprintf("%q: old_string not found%s", args.Path, fuzzyHint(body, args.OldString)),
				),
			), nil
		}
		fStart, fEnd, hits := fuzzyMatch(body, args.OldString)
		switch hits {
		case 0:
			return tools.Failure(
				call.ID,
				tools.NotFound(
					"edit",
					fmt.Sprintf("%q: old_string not found%s", args.Path, fuzzyHint(body, args.OldString)),
				),
			), nil
		case 1:
			updated := body[:fStart] + args.NewString + body[fEnd:]
			if err := t.ws.WriteFileInRoot(abs, []byte(updated), filesystem.ModePublicFile); err != nil {
				return tools.Failure(call.ID, tools.Fatal("edit", fmt.Errorf("write %q: %w", args.Path, err))), nil
			}
			effect := tools.NewFileEffect(tools.FileModify, args.Path)
			effect.File.BytesAfter = int64(len(updated))
			return tools.Success(
				call.ID,
				fmt.Sprintf("edited %s (1 replacement, matched after whitespace normalisation)", args.Path),
				effect,
			), nil
		default:
			return tools.Failure(
				call.ID,
				tools.Validation(
					"edit",
					fmt.Sprintf(
						"%q: old_string matches %d times after whitespace normalisation — tighten old_string with more context",
						args.Path,
						hits,
					),
				),
			), nil
		}
	}

	updated := strings.Replace(body, args.OldString, args.NewString, 1)
	if args.ReplaceAll {
		updated = strings.ReplaceAll(body, args.OldString, args.NewString)
	}

	if err := t.ws.WriteFileInRoot(abs, []byte(updated), filesystem.ModePublicFile); err != nil {
		return tools.Failure(call.ID, tools.Fatal("edit", fmt.Errorf("write %q: %w", args.Path, err))), nil
	}
	effect := tools.NewFileEffect(tools.FileModify, args.Path)
	effect.File.BytesAfter = int64(len(updated))
	return tools.Success(call.ID, fmt.Sprintf("edited %s (%d replacement(s))", args.Path, count), effect), nil
}

// fuzzyMatch tries to locate `old` in `body` after line-normalisation:
// per-line trailing whitespace is stripped from both sides, and \r in
// any CRLF pair is treated as whitespace. Returns the byte range
// covering the matched lines in the ORIGINAL body, plus the number of
// matches (so the caller can refuse on ambiguity).
//
// The byte range respects whether `old` had a trailing newline: if it
// did not, the trailing \n (and any \r before it) of the body's last
// matched line is excluded from the splice, so the replacement doesn't
// silently delete a newline.
func fuzzyMatch(body, old string) (int, int, int) {
	bodyLines := splitKeepingEOL(body)
	oldLines := splitKeepingEOL(old)
	if len(oldLines) == 0 {
		return 0, 0, 0
	}
	nbl := normaliseLines(bodyLines)
	nol := normaliseLines(oldLines)

	var positions []int
	for i := 0; i+len(nol) <= len(nbl); i++ {
		match := true
		for j := range nol {
			if nbl[i+j] != nol[j] {
				match = false
				break
			}
		}
		if match {
			positions = append(positions, i)
		}
	}
	if len(positions) != 1 {
		return 0, 0, len(positions)
	}

	lineStart := positions[0]
	lineEnd := lineStart + len(nol)

	byteStart := 0
	for i := range lineStart {
		byteStart += len(bodyLines[i])
	}
	byteEnd := byteStart
	for i := lineStart; i < lineEnd; i++ {
		byteEnd += len(bodyLines[i])
	}

	oldLastHasNL := strings.HasSuffix(oldLines[len(oldLines)-1], "\n")
	if !oldLastHasNL && strings.HasSuffix(bodyLines[lineEnd-1], "\n") {
		byteEnd-- // exclude trailing \n the model didn't ask to replace
		if byteEnd > 0 && body[byteEnd-1] == '\r' {
			byteEnd-- // and the \r in a CRLF pair
		}
	}
	return byteStart, byteEnd, 1
}

// fuzzyHint returns a short " (file line N has: …)" suffix for the
// "old_string not found" error when the first non-empty line of `old`
// (trimmed) appears verbatim — modulo whitespace — somewhere in
// `body`. Helps the model pinpoint the actual whitespace difference
// without burning turns on re-reads.
func fuzzyHint(body, old string) string {
	var probe string
	for _, l := range strings.Split(old, "\n") {
		l = strings.TrimSpace(strings.TrimRight(l, "\r"))
		if l != "" {
			probe = l
			break
		}
	}
	if probe == "" {
		return ""
	}
	for i, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(strings.TrimRight(l, "\r")) == probe {
			return fmt.Sprintf(" (file line %d has matching content but different whitespace: %q)", i+1, l)
		}
	}
	return ""
}

// splitKeepingEOL splits s on \n, keeping the \n attached to each line
// it terminates. "foo\nbar" -> ["foo\n","bar"]; "foo\nbar\n" ->
// ["foo\n","bar\n"]; "" -> [].
func splitKeepingEOL(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// normaliseLines strips trailing whitespace (including \r and \n) from
// each line's content. The original slice is not mutated.
func normaliseLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, " \t\r\n")
	}
	return out
}
