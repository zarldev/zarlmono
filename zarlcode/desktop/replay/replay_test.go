package replay_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/desktop/replay"
)

func TestFrames(t *testing.T) {
	t.Parallel()
	s, err := replay.New()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Frame(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Total < 20 {
		t.Fatalf("unexpected initial frame: %+v", first)
	}
	for i := range first.Total {
		frame, frameErr := s.Frame(i)
		if frameErr != nil {
			t.Fatal(frameErr)
		}
		seen := make(map[string]bool)
		for _, entry := range frame.Entries {
			if entry.ID == "" || seen[entry.ID] {
				t.Fatalf("frame %d: duplicate or missing occurrence ID %q", i, entry.ID)
			}
			seen[entry.ID] = true
		}
	}
	last, err := s.Frame(first.Total - 1)
	if err != nil {
		t.Fatal(err)
	}
	var sawDiff, sawChild, sawCompleted bool
	for _, entry := range last.Entries {
		sawDiff = sawDiff || entry.Payload.Diff != ""
		sawChild = sawChild || entry.Payload.AgentName == "tester"
		sawCompleted = sawCompleted || entry.Payload.Complete
	}
	if !sawDiff || !sawChild || !sawCompleted {
		t.Fatalf("incomplete replay: diff=%v child=%v completion=%v", sawDiff, sawChild, sawCompleted)
	}
	for _, index := range []int{-1, first.Total, 1 << 30} {
		if _, frameErr := s.Frame(index); !errors.Is(frameErr, replay.ErrFrameRange) {
			t.Fatalf("Frame(%d): got %v, want ErrFrameRange", index, frameErr)
		}
	}
}

func TestFrameIsolationAndConcurrentReads(t *testing.T) {
	t.Parallel()
	s, err := replay.New()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Frame(0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for i := range first.Total {
				frame, frameErr := s.Frame(i)
				if frameErr != nil {
					t.Error(frameErr)
					return
				}
				for j := range frame.Entries {
					frame.Entries[j].Payload.Text = "mutated"
					for k := range frame.Entries[j].Payload.Plan.Steps {
						frame.Entries[j].Payload.Plan.Steps[k].Text = "mutated"
					}
				}
			}
		})
	}
	wg.Wait()
	again, err := s.Frame(0)
	if err != nil {
		t.Fatal(err)
	}
	if again.Entries[0].Payload.Text != first.Entries[0].Payload.Text {
		t.Fatal("caller mutated a retained replay frame")
	}
}
