package transcript_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestSubagentTerminalSettlesOnlyOwnedStreams(t *testing.T) {
	for _, status := range []transcript.SubagentState{transcript.SubagentCompleted, transcript.SubagentFailed, transcript.SubagentInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			b := transcript.NewBuilder()
			b.StartTurn("root", "")
			b.AppendAssistant("root", "", "root remains active")
			child := b.StartSubagent("child", "reused-spawn", "worker", "", "", "task")
			b.AppendAssistant("child", child, "partial answer")
			b.AppendReasoning("child", child, "partial reasoning")
			sibling := b.StartSubagent("sibling", "reused-spawn", "worker", "", "", "other task")
			b.AppendAssistant("sibling", sibling, "other answer")
			b.FinishSubagent("child", status)
			revision := b.Thread().Revision()
			b.FinishSubagent("child", status)
			if b.Thread().Revision() != revision {
				t.Fatal("duplicate terminal event changed history")
			}
			for _, entry := range b.Thread().Entries() {
				if entry.Kind != transcript.EntryKinds.ENTRYASSISTANTMESSAGE && entry.Kind != transcript.EntryKinds.ENTRYREASONING {
					continue
				}
				if entry.TurnID == "child" {
					if entry.Payload.Complete != (status == transcript.SubagentCompleted) || entry.Payload.Interrupted != (status != transcript.SubagentCompleted) {
						t.Fatal("child stream lifecycle did not match terminal outcome")
					}
				} else if entry.Payload.Complete || entry.Payload.Interrupted {
					t.Fatal("child terminal event settled an unrelated stream")
				}
			}
			if _, err := b.Thread().CaptureCheckpoint(); !errors.Is(err, transcript.ErrCheckpointUnsettled) {
				t.Fatalf("capture with active sibling/root = %v", err)
			}
			b.FinishSubagent("sibling", transcript.SubagentCompleted)
			b.FinishTurn("root")
			if _, err := b.Thread().CaptureCheckpoint(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoredSubagentIgnoresDuplicateTerminalEvents(t *testing.T) {
	for _, status := range []transcript.SubagentState{transcript.SubagentCompleted, transcript.SubagentFailed, transcript.SubagentInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			b := transcript.NewBuilder()
			child := b.StartSubagent("child", "spawn", "worker", "", "", "task")
			b.AppendAssistant("child", child, "answer")
			b.AppendReasoning("child", child, "reasoning")
			b.FinishSubagent("child", status)
			b = transcript.NewBuilderFrom(b.Thread())
			revision := b.Thread().Revision()
			for _, duplicate := range []transcript.SubagentState{transcript.SubagentCompleted, transcript.SubagentFailed, transcript.SubagentInterrupted} {
				b.FinishSubagent("child", duplicate)
				if b.Thread().Revision() != revision {
					t.Fatal("restored terminal child changed after duplicate event")
				}
			}
			for _, entry := range b.Thread().Entries() {
				if entry.ID == child && entry.Payload.Subagent != status {
					t.Fatal("duplicate event replaced original terminal status")
				}
			}
		})
	}
}
