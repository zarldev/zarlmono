package code

import (
	"bytes"
	"testing"
)

func TestLineRingBuffer_AppendAndRead(t *testing.T) {
	b := newLineRingBuffer(8)
	for _, l := range []string{"one", "two", "three"} {
		b.Append([]byte(l))
	}
	lines, cur, dropped := b.ReadSince(0, 0)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if cur != 3 {
		t.Errorf("cursor = %d, want 3", cur)
	}
	if len(lines) != 3 || string(lines[0]) != "one" || string(lines[2]) != "three" {
		t.Errorf("lines = %v", linesAsStrings(lines))
	}
}

func TestLineRingBuffer_SinceCursorAdvances(t *testing.T) {
	b := newLineRingBuffer(8)
	for _, l := range []string{"a", "b", "c"} {
		b.Append([]byte(l))
	}
	// First read: full content, cursor moves to 3.
	_, cur1, _ := b.ReadSince(0, 0)
	if cur1 != 3 {
		t.Fatalf("first cursor = %d", cur1)
	}
	// Second read from cur1: nothing new yet.
	lines, cur2, _ := b.ReadSince(cur1, 0)
	if len(lines) != 0 {
		t.Errorf("expected no new lines, got %d", len(lines))
	}
	if cur2 != cur1 {
		t.Errorf("cursor changed without new appends: %d → %d", cur1, cur2)
	}
	b.Append([]byte("d"))
	lines, cur3, _ := b.ReadSince(cur2, 0)
	if len(lines) != 1 || string(lines[0]) != "d" {
		t.Errorf("expected ['d'], got %v", linesAsStrings(lines))
	}
	if cur3 != 4 {
		t.Errorf("cursor = %d, want 4", cur3)
	}
}

func TestLineRingBuffer_RotationDroppedCounter(t *testing.T) {
	b := newLineRingBuffer(3)
	for i := range 5 {
		b.Append([]byte{'L', byte('0' + i)})
	}
	// Capacity 3, wrote 5 → 2 should have rotated out.
	count, total, dropped := b.Snapshot()
	if count != 3 || total != 5 || dropped != 2 {
		t.Errorf("snapshot count=%d total=%d dropped=%d, want 3/5/2", count, total, dropped)
	}
	// Read from cursor 0 — buffer only has L2/L3/L4. Should report
	// 2 dropped + return the live window.
	lines, cur, droppedSince := b.ReadSince(0, 0)
	if droppedSince != 2 {
		t.Errorf("droppedSince = %d, want 2", droppedSince)
	}
	if cur != 5 {
		t.Errorf("cursor = %d, want 5", cur)
	}
	want := []string{"L2", "L3", "L4"}
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	for i, w := range want {
		if string(lines[i]) != w {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

func TestLineRingBuffer_DroppedBetweenReads(t *testing.T) {
	b := newLineRingBuffer(3)
	b.Append([]byte("a"))
	b.Append([]byte("b"))
	_, cur, _ := b.ReadSince(0, 0)
	if cur != 2 {
		t.Fatalf("cursor = %d, want 2", cur)
	}
	// Write 5 more — capacity 3 means b/a/c/d/e → buffer holds [c d e]
	// and the read-since-cur cursor (=2) refers to lines that have
	// rotated out.
	for _, l := range []string{"c", "d", "e", "f", "g"} {
		b.Append([]byte(l))
	}
	lines, _, droppedSince := b.ReadSince(cur, 0)
	if droppedSince != 2 {
		t.Errorf("droppedSince = %d, want 2 (c and d rotated)", droppedSince)
	}
	if len(lines) != 3 {
		t.Errorf("len(lines) = %d, want 3", len(lines))
	}
	if string(lines[0]) != "e" {
		t.Errorf("first surviving = %q, want %q", lines[0], "e")
	}
}

func TestLineRingBuffer_MaxCap(t *testing.T) {
	b := newLineRingBuffer(10)
	for i := range 8 {
		b.Append([]byte{byte('a' + i)})
	}
	// Cap return to 3 — should give the freshest 3 and advance cursor to total.
	lines, cur, _ := b.ReadSince(0, 3)
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	if string(lines[0]) != "f" || string(lines[2]) != "h" {
		t.Errorf("freshest 3 = %v, want [f g h]", linesAsStrings(lines))
	}
	if cur != 8 {
		t.Errorf("cursor = %d, want 8 (advances even when capped)", cur)
	}
}

func TestLineRingBuffer_AppendCopiesInput(t *testing.T) {
	b := newLineRingBuffer(4)
	scratch := []byte("hello")
	b.Append(scratch)
	scratch[0] = 'X' // mutate caller's slice
	lines, _, _ := b.ReadSince(0, 0)
	if !bytes.Equal(lines[0], []byte("hello")) {
		t.Errorf("buffer shares storage with caller's scratch: got %q", lines[0])
	}
}

func TestLineRingBuffer_ZeroCapFallsBack(t *testing.T) {
	b := newLineRingBuffer(0)
	if b.cap != 1024 {
		t.Errorf("cap = %d, want 1024 default", b.cap)
	}
}

func linesAsStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}
