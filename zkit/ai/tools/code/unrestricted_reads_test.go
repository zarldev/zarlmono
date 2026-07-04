package code_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestReadTool_RestrictedBlocksOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	res := execTyped(t, code.NewReadTool(ws), code.ReadArgs{Path: outside})
	if res.Success {
		t.Fatalf("expected restricted read to fail for %s", outside)
	}
	if !strings.Contains(res.Error, "outside root") && !strings.Contains(res.Error, "escapes root") {
		t.Fatalf("unexpected error: %s", res.Error)
	}
}

func TestReadTool_UnrestrictedAllowsOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\nsecond\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	res := execTyped(t, code.NewReadTool(ws, code.WithUnrestrictedReads()), code.ReadArgs{Path: outside})
	if !res.Success {
		t.Fatalf("unrestricted read failed: %s", res.Error)
	}
	body, _ := res.Data.(string)
	if !strings.Contains(body, "1\tsecret") || !strings.Contains(body, "2\tsecond") {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestReadFileHLTool_UnrestrictedAllowsOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	outside := filepath.Join(t.TempDir(), "anchor.txt")
	if err := os.WriteFile(outside, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	res := execTyped(t, code.NewReadFileHLTool(ws, code.WithUnrestrictedReads()), code.ReadFileHLArgs{Path: outside})
	if !res.Success {
		t.Fatalf("unrestricted hashline read failed: %s", res.Error)
	}
	body, _ := res.Data.(string)
	if !strings.Contains(body, "1:"+testHashlineHash("alpha", 4)+"|alpha") {
		t.Fatalf("missing anchored line in: %q", body)
	}
}

func TestLsTool_UnrestrictedAllowsOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, ".hidden"), []byte("h"), 0o644); err != nil {
		t.Fatalf("seed hidden file: %v", err)
	}

	tool := code.NewLsTool(ws, code.WithUnrestrictedReads())
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "c",
		ToolName: code.ToolNameLs,
		Arguments: tools.ToolParameters{
			"path": outsideDir,
		},
	})
	if res == nil || !res.Success {
		t.Fatalf("unrestricted ls failed: %+v", res)
	}
	body := lsText(t, res)
	if !strings.Contains(body, "a.txt") {
		t.Fatalf("outside entry missing: %q", body)
	}
	if strings.Contains(body, ".hidden") {
		t.Fatalf("hidden file should stay hidden by default: %q", body)
	}
}

func TestGrepTool_UnrestrictedAllowsOutsideWorkspace(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not installed: %v", err)
	}
	ws, _ := mustWS(t)
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "notes.txt")
	if err := os.WriteFile(outside, []byte("needle\nhay\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	tool := code.NewGrepTool(ws, code.WithUnrestrictedReads())
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "c",
		ToolName: code.ToolNameGrep,
		Arguments: tools.ToolParameters{
			"pattern": "needle",
			"path":    outsideDir,
		},
	})
	if res == nil || !res.Success {
		t.Fatalf("unrestricted grep failed: %+v", res)
	}
	body := grepText(t, res)
	if !strings.Contains(body, "notes.txt") || !strings.Contains(body, "needle") {
		t.Fatalf("unexpected grep body: %q", body)
	}
}

func TestGlobTool_UnrestrictedAllowsOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(outsideDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "nested", "b.go"), []byte("package b"), 0o644); err != nil {
		t.Fatalf("seed nested outside file: %v", err)
	}

	tool := code.NewGlobTool(ws, code.WithUnrestrictedReads())
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "c",
		ToolName: code.ToolNameGlob,
		Arguments: tools.ToolParameters{
			"pattern": "*.go",
			"root":    outsideDir,
		},
	})
	if res == nil || !res.Success {
		t.Fatalf("unrestricted glob failed: %+v", res)
	}
	body := globText(t, res)
	if !strings.Contains(body, "a.go") || !strings.Contains(body, "nested/b.go") {
		t.Fatalf("unexpected glob body: %q", body)
	}
}
