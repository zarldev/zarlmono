package openaicodex

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// sseDebugSink mirrors raw SSE event payloads to a file so the user
// can debug "why aren't reasoning chunks appearing in the live
// pane?" without waiting for an instrumented build. Activate by
// setting CODEX_DEBUG_SSE=<path> before launching zarlcode; events
// are appended (one JSON-per-line, with a leading timestamp) so
// successive runs against the same file accumulate into a session
// log.
//
// Empty / unset env var → no-op sink (zero allocation, zero I/O).
type sseDebugSink struct {
	mu sync.Mutex
	f  *os.File
}

// openSSEDebugSink consults CODEX_DEBUG_SSE and returns a sink. A
// missing or unwritable destination yields a no-op sink — debug
// instrumentation should never break the actual stream.
func openSSEDebugSink() *sseDebugSink {
	path := os.Getenv("CODEX_DEBUG_SSE")
	if path == "" {
		return &sseDebugSink{}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filesystem.ModePublicFile)
	if err != nil {
		// Silently degrade — debug aid shouldn't crash the stream.
		return &sseDebugSink{}
	}
	s := &sseDebugSink{f: f}
	// Stamp the header so multi-run logs are scannable.
	_, _ = fmt.Fprintf(f, "\n--- %s ---\n", time.Now().Format(time.RFC3339Nano))
	return s
}

// WriteEvent records one SSE event payload (the JSON blob between
// blank-line delimiters). No-op when the sink has no file. Goroutine-
// safe — multiple concurrent runs against one CODEX_DEBUG_SSE file
// interleave cleanly.
func (s *sseDebugSink) WriteEvent(payload string) {
	if s == nil || s.f == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintln(s.f, payload)
}

// Close flushes and releases the file handle. Safe on a no-op sink.
func (s *sseDebugSink) Close() {
	if s == nil || s.f == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.f.Close()
	s.f = nil
}
