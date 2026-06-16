package code

import "sync"

// lineRingBuffer holds the last N \n-delimited lines of a stream
// with O(1) append and O(unread) read-since-cursor. Used by the
// process manager to cap per-process output retention: a chatty dev
// server can run for hours without OOM'ing the shell, and the model
// only ever sees the trailing window plus a "dropped X lines"
// counter when older content rotated out.
//
// Concurrency: the buffer is goroutine-safe under its own mutex.
// Producer (the process's stdout/stderr reader) appends; consumer
// (bash_output tool calls) reads. Mutex-only is fine here because
// reads are infrequent (model-driven, ~one per turn) and appends
// are batched per stdout line — sub-microsecond critical sections.

// u64 converts a non-negative int to uint64 without a G115 warning.
// Callers guarantee v ≥ 0 (ring buffer counts and indices).
func u64(v int) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

func safeInt(v uint64) int {
	if v > uint64(maxInt) {
		return maxInt
	}
	return int(v)
}

const maxInt = int(^uint(0) >> 1)

type lineRingBuffer struct {
	mu      sync.Mutex
	cap     int
	lines   [][]byte
	head    int    // next write index
	count   int    // current live entries (≤ cap)
	cursor  uint64 // monotonic count of total lines ever appended
	dropped uint64 // count of lines that rotated out (cursor - count)
}

// newLineRingBuffer constructs a buffer with the supplied capacity.
// cap ≤ 0 falls back to a sane default (1024) rather than erroring —
// the caller is internal and a misconfiguration shouldn't break the
// process-manager spin-up path.
func newLineRingBuffer(bufCap int) *lineRingBuffer {
	if bufCap <= 0 {
		bufCap = 1024
	}
	return &lineRingBuffer{
		cap:   bufCap,
		lines: make([][]byte, bufCap),
	}
}

// Append adds a line to the buffer. When the buffer is full, the
// oldest entry rotates out and the dropped counter increments — the
// caller never blocks. line is copied so the caller can reuse its
// scratch buffer immediately after returning.
func (b *lineRingBuffer) Append(line []byte) {
	cp := make([]byte, len(line))
	copy(cp, line)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == b.cap {
		// Overwriting an existing slot — old line rotated out.
		b.dropped++
	} else {
		b.count++
	}
	b.lines[b.head] = cp
	b.head = (b.head + 1) % b.cap
	b.cursor++
}

// ReadSince returns up to max lines that arrived after the supplied
// cursor. The returned newCursor is what the caller should pass back
// on its next read to get only the lines that arrived in between.
// droppedSince counts lines that rotated out of the buffer between
// the caller's previous read and this one — non-zero means there's
// a gap the agent should know about.
//
// max ≤ 0 returns everything available since the cursor.
func (b *lineRingBuffer) ReadSince(cursor uint64, maxLines int) ([][]byte, uint64, uint64) {
	var droppedSince uint64
	b.mu.Lock()
	defer b.mu.Unlock()
	// Cursor is monotonic. The oldest still-buffered cursor value
	// is (b.cursor - b.count). Anything ≤ that fell out the back.
	oldest := b.cursor - u64(b.count)
	startCursor := cursor
	if startCursor < oldest {
		droppedSince = oldest - startCursor
		startCursor = oldest
	}
	wantCount := safeInt(b.cursor - startCursor)
	if wantCount <= 0 {
		return nil, b.cursor, droppedSince
	}
	if maxLines > 0 && wantCount > maxLines {
		// Keep the freshest `max` lines — when capped, the cursor
		// still advances to b.cursor so the caller doesn't loop
		// forever reading the same fragment.
		startCursor = b.cursor - u64(maxLines)
		wantCount = maxLines
	}
	// Translate startCursor → head-relative index in the ring.
	skip := safeInt(startCursor - oldest)
	startIdx := (b.head - b.count + skip + b.cap*2) % b.cap
	out := make([][]byte, wantCount)
	for i := range wantCount {
		idx := (startIdx + i) % b.cap
		// Defensive copy isn't necessary here — Append already
		// copied; the slice we hand back is shared with the
		// buffer's storage but never mutated.
		out[i] = b.lines[idx]
	}
	return out, b.cursor, droppedSince
}

// Snapshot returns the current state: line count, total appended,
// total dropped. Useful for list_processes and tests; cheap.
func (b *lineRingBuffer) Snapshot() (int, uint64, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count, b.cursor, b.dropped
}
