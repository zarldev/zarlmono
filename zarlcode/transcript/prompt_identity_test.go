package transcript_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestPreallocatedPromptIdentity(t *testing.T) {
	r := transcript.NewReducer()
	before, err := r.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	change, err := r.Apply(transcript.UserSubmitted{EntryID: "e10", Text: "selected prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if change.PrimaryEntryID != "e10" || r.Thread().Entries()[0].ID != "e10" {
		t.Fatal("prompt binding lost")
	}
	revision := r.Thread().Revision()
	if _, err := r.Apply(transcript.UserSubmitted{EntryID: "e10", Text: "duplicate"}); !errors.Is(err, transcript.ErrInvalidEvent) {
		t.Fatal(err)
	}
	if r.Thread().Revision() != revision {
		t.Fatal("duplicate changed transcript")
	}
	if _, err := r.Apply(transcript.UserSubmitted{Text: "next prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Thread().Validate(); err != nil {
		t.Fatal(err)
	}
	restored, err := before.Restore()
	if err != nil || !restored.IsEmpty() {
		t.Fatal("BEFORE includes submitted prompt", err)
	}
}
