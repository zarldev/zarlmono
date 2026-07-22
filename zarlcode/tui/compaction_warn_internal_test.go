package tui

import "testing"

// The manual-compaction warning fires once when the context crosses the
// trigger, stays quiet in auto mode, and re-arms after pressure drops.
func TestMaybeWarnCompaction(t *testing.T) {
	s := NewSession("", "", "")
	s.Run.window = 1000
	s.Run.pressureWindow = 1000
	s.Run.pressureReserve = 100 // trigger at 900
	s.Run.liveCtx = 950         // over the trigger

	// Auto mode: the runner compacts, so the cockpit stays quiet.
	s.AutoCompact = true
	s.maybeWarnCompaction()
	if s.Toast != "" {
		t.Fatalf("auto mode should not warn, got %q", s.Toast)
	}

	// Manual + over pressure: warns once, in the warning tone.
	s.AutoCompact = false
	s.maybeWarnCompaction()
	if s.Toast == "" || s.ToastTone != toastWarn {
		t.Fatalf("manual over-pressure should warn, got tone=%v msg=%q", s.ToastTone, s.Toast)
	}

	// Latched: still over the trigger, so it must not re-fire.
	s.Toast = ""
	s.maybeWarnCompaction()
	if s.Toast != "" {
		t.Fatalf("warning should latch while over pressure, re-fired: %q", s.Toast)
	}

	// Pressure falls back under the trigger — the latch resets.
	s.Run.liveCtx = 500
	s.maybeWarnCompaction()
	// Crossing again warns again.
	s.Run.liveCtx = 950
	s.maybeWarnCompaction()
	if s.Toast == "" {
		t.Fatal("re-crossing after a drop should warn again")
	}
}
