package tui

import "testing"

func TestRunActivityGlyphUsesBraille(t *testing.T) {
	for i, frame := range brailleActivityFrames {
		rs := []rune(frame)
		if len(rs) != 1 {
			t.Fatalf("frame %d = %q, want one rune", i, frame)
		}
		if rs[0] < '\u2800' || rs[0] > '\u28FF' {
			t.Errorf("frame %d = %q, want braille glyph", i, frame)
		}
		if got := runActivityGlyph(i, true); got != frame {
			t.Errorf("runActivityGlyph(%d, true) = %q, want %q", i, got, frame)
		}
	}

	idle := []rune(runActivityGlyph(0, false))
	if len(idle) != 1 || idle[0] < '\u2800' || idle[0] > '\u28FF' {
		t.Fatalf("idle glyph = %q, want a braille glyph", string(idle))
	}
}
