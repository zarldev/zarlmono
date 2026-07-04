package runner

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultTruncator_Passthrough(t *testing.T) {
	t.Parallel()
	in := "hello world\n"
	got := DefaultTruncator{}.Truncate(in, "echo")
	if got != in {
		t.Fatalf("small payload should pass through unchanged; got %q", got)
	}
}

func TestSpillingTruncator_BytesCap(t *testing.T) {
	t.Parallel()
	tr := SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
	big := strings.Repeat("x", defaultMaxResultBytes+8192)
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
	if keptBytes > defaultMaxResultBytes {
		t.Fatalf("kept tail %d bytes exceeds cap %d", keptBytes, defaultMaxResultBytes)
	}
	if !strings.Contains(got, "full output: ") {
		t.Fatalf("expected spill path in footer; got %s", got[footerStart:])
	}
}

func TestSpillingTruncator_LinesCap(t *testing.T) {
	t.Parallel()
	tr := SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
	lines := make([]string, defaultMaxResultLines+500)
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
	if keptLines > defaultMaxResultLines {
		t.Fatalf("kept %d lines exceeds cap %d", keptLines, defaultMaxResultLines)
	}
}

func TestDefaultTruncator_KeepsTail(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("HEADHEADHEAD\n", 5000) + "TAILSENTINEL_98765\n"
	got := DefaultTruncator{}.Truncate(body, "bash")
	if !strings.Contains(got, "TAILSENTINEL_98765") {
		t.Fatalf("expected tail sentinel preserved; not found")
	}
}

func TestDefaultTruncator_NoSpillPathInFooter(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("y", defaultMaxResultBytes+1024)
	got := DefaultTruncator{}.Truncate(in, "tool")
	if !strings.Contains(got, "[truncated by") {
		t.Fatalf("expected truncation footer")
	}
	if strings.Contains(got, "full output:") {
		t.Fatalf("DefaultTruncator should NOT spill — footer mentions a path: %s", got[len(got)-200:])
	}
}

func TestSpillingTruncator_WritesFullPayload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := strings.Repeat("y", defaultMaxResultBytes+1024)
	tr := SpillingTruncator{Dir: dir, Prefix: "test-"}
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

func TestSanitizeForFilename(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"bash", "bash"},
		{"my_tool-v2", "my_tool-v2"},
		{"weird/slashes\\and spaces", "weirdslashesandspaces"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeForFilename(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{50 * 1024, "50.0KB"},
		{1024 * 1024, "1.0MB"},
	}
	for _, c := range cases {
		if got := formatSize(c.n); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// Cleanup removes the truncator's per-session spill directory. The
// roast called the SpillingTruncator's persistent files a "leak";
// this test pins the explicit-cleanup contract that closes that
// gap — long-running agents must be able to sweep their own
// /tmp footprint at shutdown.
func TestSpillingTruncator_CleanupRemovesSessionDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tr := &SpillingTruncator{
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
	tr := &SpillingTruncator{Dir: t.TempDir(), Prefix: "test-"}
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
