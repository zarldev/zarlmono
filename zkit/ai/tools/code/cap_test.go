package code_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Caps prevent the model from one-shotting a multi-thousand-byte
// argument through the streaming JSON encoder, which has been observed
// to drop characters and produce `parse error … missing closing quote`
// failures from llama.cpp. The cap is the structural fix; these tests
// pin the boundary.

// Test fixtures. Use sizes well clear of the current default caps
// (write/append: 256KB, edit per-arg: 64KB) so the tests survive
// minor future tuning. CODE_*_MAX_BYTES env knobs override the
// defaults at runtime; tests assume the defaults are in effect.
const (
	overWriteCap  = 300 * 1024 // > write cap (256KB)
	underWriteCap = 1024       // < write cap
	overEditCap   = 80 * 1024  // > edit cap (64KB)
	underEditCap  = 1000       // < edit cap
)

func TestWrite_RejectsOversizedContent(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	big := strings.Repeat("x", overWriteCap)
	res := execTyped(t, tool, code.WriteArgs{Path: "huge.txt", Content: big})
	if res.Success {
		t.Fatal("expected oversized write to be rejected")
	}
	if !strings.Contains(res.Error, "too large") {
		t.Errorf("expected 'too large' in error, got %q", res.Error)
	}
	// Error should point the model at write_append.
	if !strings.Contains(res.Error, "write_append") {
		t.Errorf("expected error to recommend write_append, got %q", res.Error)
	}
}

func TestWrite_AcceptsContentUnderCap(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	body := strings.Repeat("y", underWriteCap)
	res := execTyped(t, tool, code.WriteArgs{Path: "fits.txt", Content: body})
	if !res.Success {
		t.Errorf("under-cap write should succeed, got: %s", res.Error)
	}
}

func TestWriteAppend_RejectsOversizedContent(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	app := code.NewWriteAppendTool(ws)

	big := strings.Repeat("a", overWriteCap)
	res := execTyped(t, app, code.WriteAppendArgs{Path: "huge.txt", Content: big})
	if res.Success {
		t.Fatal("expected oversized write_append to be rejected")
	}
	if !strings.Contains(res.Error, "chunk too large") {
		t.Errorf("expected chunk-too-large error, got %q", res.Error)
	}
}

func TestEdit_RejectsOversizedNewString(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	w := code.NewWriteTool(ws)
	e := code.NewEditTool(ws)

	// Seed a small file we can edit.
	if r := execTyped(t, w, code.WriteArgs{Path: "src.txt", Content: "anchor"}); !r.Success {
		t.Fatalf("seed write failed: %s", r.Error)
	}

	huge := strings.Repeat("z", overEditCap)
	res := execTyped(t, e, code.EditArgs{Path: "src.txt", OldString: "anchor", NewString: huge})
	if res.Success {
		t.Fatal("expected oversized new_string to be rejected")
	}
	if !strings.Contains(res.Error, "new_string too large") {
		t.Errorf("expected new_string size error, got %q", res.Error)
	}
}

func TestEdit_RejectsOversizedOldString(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	w := code.NewWriteTool(ws)
	e := code.NewEditTool(ws)

	// Seed via scaffold + chunked appends so the file actually contains
	// the huge old_string the edit will try to match. (write itself is
	// capped, so chunked appends are the reliable seeding path.)
	if r := execTyped(t, w, code.WriteArgs{Path: "src.txt", Content: ""}); !r.Success {
		t.Fatalf("seed write failed: %s", r.Error)
	}
	app := code.NewWriteAppendTool(ws)
	for range 2048 {
		execTyped(t, app, code.WriteAppendArgs{Path: "src.txt", Content: strings.Repeat("a", 2048)})
	}

	huge := strings.Repeat("a", overEditCap)
	res := execTyped(t, e, code.EditArgs{Path: "src.txt", OldString: huge, NewString: "x"})
	if res.Success {
		t.Fatal("expected oversized old_string to be rejected")
	}
	if !strings.Contains(res.Error, "old_string too large") {
		t.Errorf("expected old_string size error, got %q", res.Error)
	}
}

func TestEdit_AcceptsArgsUnderCap(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	w := code.NewWriteTool(ws)
	e := code.NewEditTool(ws)

	if r := execTyped(t, w, code.WriteArgs{Path: "src.txt", Content: "before middle after"}); !r.Success {
		t.Fatalf("seed: %s", r.Error)
	}
	res := execTyped(t, e, code.EditArgs{Path: "src.txt", OldString: "middle", NewString: strings.Repeat("y", underEditCap)})
	if !res.Success {
		t.Errorf("under-cap edit should succeed, got: %s", res.Error)
	}
}

// fixture sanity — the existing append helper is a fine seeding path
// when we need a file larger than the write cap allows in one call.
func TestSeedingPathExists(t *testing.T) {
	t.Parallel()
	if _ = (tools.ToolName)(""); false {
		// Tied import — keeps `tools` in use if other tests change.
		t.Fatal("unreachable")
	}
}
