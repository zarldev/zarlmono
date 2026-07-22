package code_test

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestReadFileHLTool_RendersBase64SHA256Anchors(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("line1\r\nline2  \n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewReadFileHLTool(ws), code.ReadFileHLArgs{Path: "hello.txt"})
	if !res.Success {
		t.Fatalf("read failed: %s", res.Error)
	}
	body, _ := res.Data.(string)
	if !strings.Contains(body, "1:"+testHashlineHash("line1", 4)+"|line1") {
		t.Fatalf("missing CRLF-normalized first hashline in:\n%s", body)
	}
	if !strings.Contains(body, "2:"+testHashlineHash("line2  ", 4)+"|line2  ") {
		t.Fatalf("missing trailing-space-sensitive second hashline in:\n%s", body)
	}
}

func TestEditFileHLTool_BatchEditsApplyAtomically(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "batch.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "batch.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 2,
				StartHash: testHashlineHash("two", 4),
				EndLine:   3,
				EndHash:   testHashlineHash("three", 4),
				NewString: "TWO-THREE\n",
			},
			{
				StartLine: 4,
				StartHash: testHashlineHash("four", 4),
				Mode:      "insert_after",
				NewString: "after four\n",
			},
			{
				StartLine: 5,
				StartHash: testHashlineHash("five", 4),
				Mode:      "delete",
			},
		},
	})
	if !res.Success {
		t.Fatalf("batch edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\nTWO-THREE\nfour\nafter four\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_BatchIgnoresEmptyTopLevelFields(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "batch-empty-top-level.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := code.NewEditFileHLTool(ws).Execute(t.Context(), tools.ToolCall{
		ID:       "test-call",
		ToolName: code.ToolNameEdit,
		Arguments: tools.ToolParameters{
			"path":       "batch-empty-top-level.txt",
			"start_line": 0,
			"start_hash": "",
			"end_line":   0,
			"end_hash":   "",
			"new_string": "",
			"mode":       "replace",
			"edits": []any{
				map[string]any{
					"start_line": 1,
					"start_hash": testHashlineHash("one", 4),
					"new_string": "ONE\n",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("batch edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "ONE\ntwo\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_BatchRejectsStaleWithoutWriting(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "batch-stale.txt")
	original := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "batch-stale.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 1,
				StartHash: testHashlineHash("one", 4),
				NewString: "ONE\n",
			},
			{
				StartLine: 3,
				StartHash: testHashlineHash("missing", 4),
				NewString: "THREE\n",
			},
		},
	})
	if res.Success {
		t.Fatal("expected stale batch failure")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.STALE {
		t.Fatalf("expected STALE kind, got %v (%s)", res.Err, res.Error)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("file changed on failed batch edit: %q", got)
	}
}

// A replace whose original line ended in a newline stays newline-terminated
// even when new_string omits the trailing newline — the tool must not
// silently un-terminate the line or drop the file's final newline.
func TestEditFileHLTool_PreservesTrailingNewline(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "nl.txt")

	// Replace a middle line without a trailing newline: must not merge lines.
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path:  "nl.txt",
		Edits: []code.HashlineEdit{{StartLine: 2, StartHash: testHashlineHash("two", 4), NewString: "TWO"}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\nTWO\nthree\n" {
		t.Fatalf("mid-line replace = %q, want newline preserved", got)
	}

	// Replace the last line without a trailing newline: file keeps its final \n.
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res = execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path:  "nl.txt",
		Edits: []code.HashlineEdit{{StartLine: 3, StartHash: testHashlineHash("three", 4), NewString: "THREE"}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\ntwo\nTHREE\n" {
		t.Fatalf("last-line replace = %q, want trailing newline preserved", got)
	}

	// A file with no final newline stays that way (nothing to preserve).
	if err := os.WriteFile(path, []byte("one\ntwo\nthree"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res = execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path:  "nl.txt",
		Edits: []code.HashlineEdit{{StartLine: 3, StartHash: testHashlineHash("three", 4), NewString: "THREE"}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\ntwo\nTHREE" {
		t.Fatalf("no-EOL file replace = %q, want no trailing newline added", got)
	}

	// An explicit trailing newline in new_string is not doubled.
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res = execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path:  "nl.txt",
		Edits: []code.HashlineEdit{{StartLine: 2, StartHash: testHashlineHash("two", 4), NewString: "TWO\n"}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\nTWO\nthree\n" {
		t.Fatalf("explicit newline = %q, want single newline", got)
	}
}

func TestEditFileHLTool_ReturnsFreshAnchorWindow(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "window.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\nsix\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Replace a single line with two lines, shifting everything below it.
	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "window.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 3,
				StartHash: testHashlineHash("three", 4),
				NewString: "THREE-A\nTHREE-B\n",
			},
		},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	msg, _ := res.Data.(string)
	if !strings.Contains(msg, "fresh anchors") {
		t.Fatalf("missing fresh-anchor window in:\n%s", msg)
	}
	// The replacement lands at its new line numbers with recomputed hashes.
	if !strings.Contains(msg, "3:"+testHashlineHash("THREE-A", 4)+"|THREE-A") {
		t.Fatalf("missing anchor for new line 3 in:\n%s", msg)
	}
	// A line below the splice reports its shifted number, not the stale one —
	// this is what lets the model keep editing without re-reading.
	if !strings.Contains(msg, "5:"+testHashlineHash("four", 4)+"|four") {
		t.Fatalf("expected 'four' anchored at shifted line 5 in:\n%s", msg)
	}
}

func TestEditFileHLTool_DeleteWindowAnchorsSurvivingLines(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "window-delete.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "window-delete.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 2,
				StartHash: testHashlineHash("two", 4),
				Mode:      "delete",
			},
		},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	msg, _ := res.Data.(string)
	// "three" moved up to line 2 after the delete; the window proves it.
	if !strings.Contains(msg, "2:"+testHashlineHash("three", 4)+"|three") {
		t.Fatalf("expected 'three' anchored at shifted line 2 in:\n%s", msg)
	}
}

func TestEditFileHLTool_LargeReplacementSkipsWindow(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "window-large.txt")
	if err := os.WriteFile(path, []byte("anchor\ntail\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A replacement bigger than the window cap (80 lines) should not reflate
	// the result into a full dump — the summary stands alone and the model
	// re-reads instead.
	big := strings.Repeat("x\n", 200)
	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "window-large.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 1,
				StartHash: testHashlineHash("anchor", 4),
				NewString: big,
			},
		},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if msg, _ := res.Data.(string); strings.Contains(msg, "fresh anchors") {
		t.Fatalf("expected no window past the line cap, got:\n%s", msg)
	}
}

func TestEditFileHLTool_BatchRejectsOverlappingRanges(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "batch-overlap.txt")
	original := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "batch-overlap.txt",
		Edits: []code.HashlineEdit{
			{
				StartLine: 1,
				StartHash: testHashlineHash("one", 4),
				EndLine:   3,
				EndHash:   testHashlineHash("three", 4),
				NewString: "A\n",
			},
			{
				StartLine: 2,
				StartHash: testHashlineHash("two", 4),
				EndLine:   4,
				EndHash:   testHashlineHash("four", 4),
				NewString: "B\n",
			},
		},
	})
	if res.Success {
		t.Fatal("expected overlapping batch failure")
	}
	if !strings.Contains(res.Error, "overlap") {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("file changed on failed batch edit: %q", got)
	}
}

func TestReadFileHLTool_AllowsThreeCharacterHashes(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	if err := os.WriteFile(filepath.Join(root, "short.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewReadFileHLTool(ws), code.ReadFileHLArgs{Path: "short.txt", HashLen: 3})
	if !res.Success {
		t.Fatalf("read failed: %s", res.Error)
	}
	body, _ := res.Data.(string)
	if !strings.Contains(body, "1:"+testHashlineHash("alpha", 3)+"|alpha") {
		t.Fatalf("missing 3-char hashline in:\n%s", body)
	}
}

func TestReadFileHLTool_RejectsOtherHashLengths(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	if err := os.WriteFile(filepath.Join(root, "bad.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewReadFileHLTool(ws), code.ReadFileHLArgs{Path: "bad.txt", HashLen: 2})
	if res.Success {
		t.Fatal("expected invalid hash_len failure")
	}
	if !strings.Contains(res.Error, "hash_len must be 3 or 4") {
		t.Fatalf("unexpected error: %s", res.Error)
	}
}

func TestEditFileHLTool_ReplacesAmbiguousContentByAnchor(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "dupes.txt")
	if err := os.WriteFile(path, []byte("same\nsame\nsame\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "dupes.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("same", 4),
			NewString: "middle\n",
		}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	got := readFile(t, path)
	if got != "same\nmiddle\nsame\n" {
		t.Fatalf("content = %q", got)
	}
	assertOneFileEffect(t, res, tools.FileModify, "dupes.txt")
}

func TestEditFileHLTool_ReplacesRangeWithThreeCharacterHashes(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "range.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "range.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("two", 3),
			EndLine:   3,
			EndHash:   testHashlineHash("three", 3),
			NewString: "TWO-THREE\n",
		}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\nTWO-THREE\nfour\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_RejectsStaleHashWithoutWriting(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "stale.txt")
	original := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "stale.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("old beta", 4),
			NewString: "BETA\n",
		}},
	})
	if res.Success {
		t.Fatal("expected stale hash rejection")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.STALE {
		t.Fatalf("expected STALE kind, got %v (%s)", res.Err, res.Error)
	}
	if !strings.Contains(res.Error, "re-run read on this file") {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("file changed on failed edit: %q", got)
	}
}

func TestEditFileHLTool_RecoversShiftedAnchorByHash(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "shift.txt")
	// "target" sat at line 3 when read; an earlier edit inserted two lines
	// above it, so it is now at line 5. The stale line-3 anchor must still
	// resolve via its content hash.
	if err := os.WriteFile(path, []byte("ins1\nins2\nzero\none\ntarget\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "shift.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 3,
			StartHash: testHashlineHash("target", 4),
			NewString: "TARGET\n",
		}},
	})
	if !res.Success {
		t.Fatalf("edit failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "ins1\nins2\nzero\none\nTARGET\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_AmbiguousDriftedDuplicateNeedsReread(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "ambig.txt")
	// The line-2 anchor's content changed, and "same" now sits at lines 1
	// and 3 — equally far from the anchor. Guessing could edit the wrong
	// line, so the tool must refuse and ask for a fresh read.
	if err := os.WriteFile(path, []byte("same\nchanged\nsame\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "ambig.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("same", 4),
			NewString: "X\n",
		}},
	})
	if res.Success {
		t.Fatalf("expected ambiguous-anchor refusal, file now: %q", readFile(t, path))
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.STALE {
		t.Fatalf("expected STALE kind, got %v (%s)", res.Err, res.Error)
	}
}

func TestEditFileHLTool_InsertAndDelete(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "ops.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := code.NewEditFileHLTool(ws)

	res := execTyped(t, tool, code.EditFileHLArgs{
		Path: "ops.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("two", 4),
			Mode:      "insert_before",
			NewString: "before two\n",
		}},
	})
	if !res.Success {
		t.Fatalf("insert_before failed: %s", res.Error)
	}

	res = execTyped(t, tool, code.EditFileHLArgs{
		Path: "ops.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 1,
			StartHash: testHashlineHash("one", 4),
			Mode:      "insert_after",
			NewString: "after one\n",
		}},
	})
	if !res.Success {
		t.Fatalf("insert_after failed: %s", res.Error)
	}

	res = execTyped(t, tool, code.EditFileHLArgs{
		Path: "ops.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 5,
			StartHash: testHashlineHash("three", 4),
			Mode:      "delete",
		}},
	})
	if !res.Success {
		t.Fatalf("delete failed: %s", res.Error)
	}

	if got := readFile(t, path); got != "one\nafter one\nbefore two\ntwo\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_InsertAllowsRedundantEndAnchor(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	path := filepath.Join(root, "insert-redundant-end.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "insert-redundant-end.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 2,
			StartHash: testHashlineHash("two", 4),
			EndLine:   2,
			EndHash:   testHashlineHash("two", 4),
			Mode:      "insert_before",
			NewString: "before two\n",
		}},
	})
	if !res.Success {
		t.Fatalf("insert_before failed: %s", res.Error)
	}
	if got := readFile(t, path); got != "one\nbefore two\ntwo\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileHLTool_RejectsInvalidAnchorShape(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	if err := os.WriteFile(filepath.Join(root, "shape.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := execTyped(t, code.NewEditFileHLTool(ws), code.EditFileHLArgs{
		Path: "shape.txt",
		Edits: []code.HashlineEdit{{
			StartLine: 1,
			StartHash: "aa",
			NewString: "beta\n",
		}},
	})
	if res.Success {
		t.Fatal("expected invalid hash failure")
	}
	if !strings.Contains(res.Error, "start_hash must be 3 or 4") {
		t.Fatalf("unexpected error: %s", res.Error)
	}
}

func testHashlineHash(content string, length int) string {
	sum := sha256.Sum256([]byte(content))
	return base64.RawStdEncoding.EncodeToString(sum[:])[:length]
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
