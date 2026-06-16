package engine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// TestRunHeadlessVerifiedLoop_Live proves the verified re-drive end-to-end
// against a real llama-server: a workspace with a failing Go test, `go test`
// as the oracle, and the agent re-driven until the suite passes. Gated on
// LLAMACPP_LIVE_URL so CI and offline runs skip it.
func TestRunHeadlessVerifiedLoop_Live(t *testing.T) {
	base := os.Getenv("LLAMACPP_LIVE_URL")
	if base == "" {
		t.Skip("LLAMACPP_LIVE_URL not set — skipping live verified-loop test")
	}

	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module verifyloop\n\ngo 1.26\n")
	write("adder.go", "package verifyloop\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a - b // BUG: subtracts\n}\n")
	write("adder_test.go", "package verifyloop\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2,3) = %d, want 5\", got)\n\t}\n}\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	prov, err := llamacpp.NewProvider(llamacpp.WithBaseURL(base))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := engine.NewLiveRunner(prov, ws, nil, "local")
	live.SetVerifyLoop("go test ./...", 3)
	live.SetContextWindow(131072)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	res := live.RunHeadless(ctx, "The Go test in this repo fails. Find the bug in the source (not the test) and fix it.", 15)
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("terminal reason = %s (err %v), want completed", res.Reason, res.Err)
	}

	// The oracle is the truth: the suite must actually pass now.
	post := exec.CommandContext(ctx, "go", "test", "./...")
	post.Dir = root
	if out, err := post.CombinedOutput(); err != nil {
		t.Fatalf("post-run go test failed — verified loop reported done on a red suite: %v\n%s", err, out)
	}
}
