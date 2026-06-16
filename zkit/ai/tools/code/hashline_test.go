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
		Path:      "dupes.txt",
		StartLine: 2,
		StartHash: testHashlineHash("same", 4),
		NewString: "middle\n",
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
		Path:      "range.txt",
		StartLine: 2,
		StartHash: testHashlineHash("two", 3),
		EndLine:   3,
		EndHash:   testHashlineHash("three", 3),
		NewString: "TWO-THREE\n",
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
		Path:      "stale.txt",
		StartLine: 2,
		StartHash: testHashlineHash("old beta", 4),
		NewString: "BETA\n",
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
		Path:      "shift.txt",
		StartLine: 3,
		StartHash: testHashlineHash("target", 4),
		NewString: "TARGET\n",
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
		Path:      "ambig.txt",
		StartLine: 2,
		StartHash: testHashlineHash("same", 4),
		NewString: "X\n",
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
		Path:      "ops.txt",
		StartLine: 2,
		StartHash: testHashlineHash("two", 4),
		Mode:      "insert_before",
		NewString: "before two\n",
	})
	if !res.Success {
		t.Fatalf("insert_before failed: %s", res.Error)
	}

	res = execTyped(t, tool, code.EditFileHLArgs{
		Path:      "ops.txt",
		StartLine: 1,
		StartHash: testHashlineHash("one", 4),
		Mode:      "insert_after",
		NewString: "after one\n",
	})
	if !res.Success {
		t.Fatalf("insert_after failed: %s", res.Error)
	}

	res = execTyped(t, tool, code.EditFileHLArgs{
		Path:      "ops.txt",
		StartLine: 5,
		StartHash: testHashlineHash("three", 4),
		Mode:      "delete",
	})
	if !res.Success {
		t.Fatalf("delete failed: %s", res.Error)
	}

	if got := readFile(t, path); got != "one\nafter one\nbefore two\ntwo\n" {
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
		Path:      "shape.txt",
		StartLine: 1,
		StartHash: "aa",
		NewString: "beta\n",
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
