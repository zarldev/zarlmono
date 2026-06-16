package runner

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Tool result truncation. A single oversized tool result (e.g.
// `find /` or reading a 200KB file) can blow past the model's
// context window in one shot. The runner tail-caps every tool
// result before it joins the conversation — errors and exit codes
// are usually at the end, so the tail is the signal-rich part.
//
// Trimming policy is injected via Truncator. Consumers that want a
// debug-friendly footer pointing at a spilled-to-disk full transcript
// install SpillingTruncator (the zarlcode pattern); consumers that
// can't write temp files (zarlai serving HTTP requests) install
// DefaultTruncator and accept that the head is unrecoverable from
// the conversation alone.

const (
	defaultMaxResultBytes = 50 * 1024
	defaultMaxResultLines = 2000
)

// Truncator caps the size of a tool-result string before it joins
// the message history. The toolName lets a Truncator tag any
// out-of-band artefacts (like a spill file's name) so a human can
// trace which call produced what; an in-memory implementation
// ignores it.
//
// # Concurrency
//
// Truncate runs from arbitrary goroutines under WithToolConcurrency
// — the dispatch goroutine that ran the tool calls Truncate before
// appending to the message history. Implementations MUST be safe
// for concurrent calls. The shipped Default and SpillingTruncator
// are; the latter relies on os.CreateTemp's atomicity for unique
// spill paths.
type Truncator interface {
	Truncate(text, toolName string) string
}

// DefaultTruncator trims tail-only with no out-of-band spill. The
// runner's default. MaxBytes/MaxLines of 0 use the package defaults
// (50KB / 2000 lines).
type DefaultTruncator struct {
	MaxBytes int
	MaxLines int
}

// Truncate keeps the tail of s, capped at MaxBytes/MaxLines (50KB /
// 2000 lines when zero), and appends a footer noting what was cut.
// The head is discarded — no spill file. Ignores toolName.
func (t DefaultTruncator) Truncate(s, _ string) string {
	return trimWithFooter(s, t.bytes(), t.lines(), "")
}

func (t DefaultTruncator) bytes() int { return cmp.Or(t.MaxBytes, defaultMaxResultBytes) }
func (t DefaultTruncator) lines() int { return cmp.Or(t.MaxLines, defaultMaxResultLines) }

// SpillingTruncator trims AND writes the original to disk so a
// follow-up bash can grep/head it. The footer of the returned text
// points at the spill file. Spill failures are non-fatal — the trim
// still happens; the footer just omits the path.
//
// # Lifetime
//
// Every truncator instance lazily creates a private subdirectory
// (under Dir, or os.TempDir() when Dir is empty) on the first call
// that actually spills. All this instance's spills land in that
// subdirectory; [SpillingTruncator.Cleanup] removes the directory
// in one shot.
//
// Use the pointer form so the lazy-init state survives across
// calls — `runner.WithResultTruncator(&runner.SpillingTruncator{
// Prefix: "zarlcode-"})`. Call .Cleanup() in the consumer's
// shutdown path so a long-running agent doesn't accumulate spill
// files indefinitely.
type SpillingTruncator struct {
	MaxBytes int
	MaxLines int
	Dir      string // base directory for the session subdir (empty = os.TempDir())
	Prefix   string // filename prefix for the spill (e.g. "zarlcode-")

	once       sync.Once
	sessionDir string // populated by ensureSessionDir; removed by Cleanup
}

// ensureSessionDir lazily creates the per-instance subdirectory the
// truncator owns. Falls back to t.Dir (or os.TempDir() if that's
// also empty) when MkdirTemp fails, which means cleanup degrades to
// "you can still find the files but they're mixed with other
// runtime spills" — not a leak, just less surgical. Safe to call
// concurrently via sync.Once.
func (t *SpillingTruncator) ensureSessionDir() string {
	t.once.Do(func() {
		base := cmp.Or(t.Dir, os.TempDir())
		px := cmp.Or(t.Prefix, "tool-")
		dir, err := os.MkdirTemp(base, px+"session-*")
		if err != nil {
			t.sessionDir = base
			return
		}
		t.sessionDir = dir
	})
	return t.sessionDir
}

// Truncate keeps the tail of s, capped at MaxBytes/MaxLines (50KB /
// 2000 lines when zero). Before trimming it writes the full original
// to a file in the lazily created session directory and points the
// footer at it; a spill failure is non-fatal — the trim still
// happens, the footer just omits the path. Results under both caps
// pass through untouched. Pointer receiver so the lazy session-dir
// init via sync.Once survives across calls.
func (t *SpillingTruncator) Truncate(s, toolName string) string {
	mb := cmp.Or(t.MaxBytes, defaultMaxResultBytes)
	ml := cmp.Or(t.MaxLines, defaultMaxResultLines)
	if !needsTrim(s, mb, ml) {
		return s
	}
	spill := spillToDisk(s, toolName, t.ensureSessionDir(), cmp.Or(t.Prefix, "tool-"))
	return trimWithFooter(s, mb, ml, spill)
}

// Cleanup removes the per-instance spill directory and every file
// in it. Idempotent — a second call (or one before any spill
// happened) is a no-op. Best-effort: a non-nil return is
// informational. Call from the consumer's shutdown path; without
// it a long-running agent leaves spill files in os.TempDir()
// indefinitely.
//
// Guards against removing a shared base directory: if
// ensureSessionDir fell back to t.Dir / os.TempDir() (MkdirTemp
// failed), Cleanup is a no-op rather than nuking a parent we don't
// own.
func (t *SpillingTruncator) Cleanup() error {
	if t.sessionDir == "" {
		return nil
	}
	// Only remove a directory the truncator created (the MkdirTemp
	// path). When ensureSessionDir fell back to the user-supplied
	// base directory, leave it alone — that's a directory we don't
	// own.
	base := cmp.Or(t.Dir, os.TempDir())
	if t.sessionDir == base {
		return nil
	}
	err := os.RemoveAll(t.sessionDir)
	t.sessionDir = ""
	return err
}

func needsTrim(s string, maxBytes, maxLines int) bool {
	if s == "" {
		return false
	}
	if len(s) > maxBytes {
		return true
	}
	if strings.Count(s, "\n")+1 > maxLines {
		return true
	}
	return false
}

func trimWithFooter(s string, maxBytes, maxLines int, spillPath string) string {
	if !needsTrim(s, maxBytes, maxLines) {
		return s
	}
	totalBytes := len(s)
	totalLines := strings.Count(s, "\n") + 1

	kept := s
	cause := ""
	if totalLines > maxLines {
		lines := strings.Split(s, "\n")
		kept = strings.Join(lines[len(lines)-maxLines:], "\n")
		cause = "lines"
	}
	if len(kept) > maxBytes {
		// Keep tail; align to a newline boundary so we don't cut a
		// UTF-8 codepoint or leave a half-line at the top.
		kept = kept[len(kept)-maxBytes:]
		if i := strings.IndexByte(kept, '\n'); i >= 0 && i < len(kept)-1 {
			kept = kept[i+1:]
		}
		cause = "bytes"
	}

	keptLines := strings.Count(kept, "\n") + 1
	footer := fmt.Sprintf(
		"\n\n[truncated by %s: %s / %d lines → %s / %d lines (kept tail)",
		cause, formatSize(totalBytes), totalLines, formatSize(len(kept)), keptLines,
	)
	if spillPath != "" {
		footer += fmt.Sprintf("; full output: %s — bash can grep/head it]", spillPath)
	} else {
		footer += "]"
	}
	return kept + footer
}

func spillToDisk(s, toolName, dir, prefix string) string {
	safe := cmp.Or(sanitizeForFilename(toolName), "tool")
	f, err := os.CreateTemp(dir, prefix+safe+"-*.log")
	if err != nil {
		return ""
	}
	if _, err := f.WriteString(s); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return ""
	}
	if err := f.Close(); err != nil {
		return ""
	}
	return f.Name()
}

func sanitizeForFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			// drop
		}
		if b.Len() >= 32 {
			break
		}
	}
	return b.String()
}

func formatSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
