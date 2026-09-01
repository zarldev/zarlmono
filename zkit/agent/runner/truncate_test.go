package runner_test

import (
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestDefaultTruncator_Passthrough(t *testing.T) {
	t.Parallel()
	in := "hello world\n"
	got := runner.DefaultTruncator{}.Truncate(in, "echo")
	if got != in {
		t.Fatalf("small payload should pass through unchanged; got %q", got)
	}
}

func TestSpillingTruncator_BytesCap(t *testing.T) {
	t.Parallel()
	tr := runner.SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
	big := strings.Repeat("x", 50*1024+8192)
	got := tr.Truncate(big, "huge")
	if !strings.Contains(got, "[truncated by") {
		t.Fatalf("expected truncation footer; got tail %q", got[len(got)-200:])
	}
	// Footer adds bytes — kept-text portion should be under the cap.
	footerStart := strings.LastIndex(got, "\n\n[truncated")
	if footerStart < 0 {
		t.Fatalf("no footer in output")
	}
	keptBytes := footerStart
	if keptBytes > 50*1024 {
		t.Fatalf("kept tail %d bytes exceeds cap %d", keptBytes, 50*1024)
	}
	if !strings.Contains(got, "full output: ") {
		t.Fatalf("expected spill path in footer; got %s", got[footerStart:])
	}
}

func TestSpillingTruncator_LinesCap(t *testing.T) {
	t.Parallel()
	tr := runner.SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
	lines := make([]string, 2000+500)
	for i := range lines {
		lines[i] = "line"
	}
	in := strings.Join(lines, "\n")
	got := tr.Truncate(in, "find")
	if !strings.Contains(got, "[truncated by lines:") {
		t.Fatalf("expected lines-cause truncation footer; got %s", got[len(got)-300:])
	}
	footerStart := strings.LastIndex(got, "\n\n[truncated")
	body := got[:footerStart]
	keptLines := strings.Count(body, "\n") + 1
	if keptLines > 2000 {
		t.Fatalf("kept %d lines exceeds cap %d", keptLines, 2000)
	}
}

func TestDefaultTruncator_KeepsTail(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("HEADHEADHEAD\n", 5000) + "TAILSENTINEL_98765\n"
	got := runner.DefaultTruncator{}.Truncate(body, "bash")
	if !strings.Contains(got, "TAILSENTINEL_98765") {
		t.Fatalf("expected tail sentinel preserved; not found")
	}
}

func TestDefaultTruncator_NoSpillPathInFooter(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("y", 50*1024+1024)
	got := runner.DefaultTruncator{}.Truncate(in, "tool")
	if !strings.Contains(got, "[truncated by") {
		t.Fatalf("expected truncation footer")
	}
	if strings.Contains(got, "full output:") {
		t.Fatalf("runner.DefaultTruncator should NOT spill — footer mentions a path: %s", got[len(got)-200:])
	}
}

func TestSpillingTruncator_WritesFullPayload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := strings.Repeat("y", 50*1024+1024)
	tr := runner.SpillingTruncator{Dir: dir, Prefix: "test-"}
	got := tr.Truncate(in, "bashy")
	// Extract the spill path from the footer.
	const marker = "full output: "
	_, after, ok := strings.Cut(got, marker)
	if !ok {
		t.Fatalf("no spill path in footer: %s", got[len(got)-200:])
	}
	rest := after
	end := strings.IndexAny(rest, " ")
	if end < 0 {
		t.Fatalf("malformed footer")
	}
	path := rest[:end]
	t.Cleanup(func() { _ = os.Remove(path) })
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(full) != in {
		t.Fatalf("spill content mismatch (len got=%d want=%d)", len(full), len(in))
	}
	if !strings.Contains(path, "bashy") {
		t.Errorf("spill path %q should include tool name 'bashy'", path)
	}
}

// Cleanup removes the truncator's per-session spill directory. The
// roast called the runner.SpillingTruncator's persistent files a "leak";
// this test pins the explicit-cleanup contract that closes that
// gap — long-running agents must be able to sweep their own
// /tmp footprint at shutdown.
func TestSpillingTruncator_CleanupRemovesSessionDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tr := &runner.SpillingTruncator{
		Dir:      tmp,
		Prefix:   "test-",
		MaxBytes: 16, // tiny cap so any string triggers a spill
	}
	got := tr.Truncate("this is more than 16 bytes of content to force a spill", "bash")
	if !strings.Contains(got, "full output:") {
		t.Fatalf("expected truncated result to reference spill path: %q", got)
	}
	// The session dir lives under tmp; ensure something was created.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read tmp: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no session dir created in tmp")
	}
	// Cleanup removes it.
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	entries, err = os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read tmp after cleanup: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("session dir survived Cleanup; tmp still has %d entries", len(entries))
	}
}

func TestSpillingTruncator_CleanupIdempotent(t *testing.T) {
	t.Parallel()
	tr := &runner.SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
	// Cleanup before any spill is a no-op.
	if err := tr.Cleanup(); err != nil {
		t.Errorf("Cleanup with no spills should be no-op, got %v", err)
	}
	// And second Cleanup is fine too.
	_ = tr.Truncate(strings.Repeat("x", 64*1024+1), "bash")
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("first Cleanup: %v", err)
	}
	if err := tr.Cleanup(); err != nil {
		t.Errorf("second Cleanup should be no-op, got %v", err)
	}
}
