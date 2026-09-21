package rewind_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestExactBoundarySettlementAndInitialProvenance(t *testing.T) {
	t.Parallel()
	initial := rewind.InitialContinuation{CheckpointID: "origin", CheckpointChecksum: "checksum"}
	for _, tt := range []struct {
		name      string
		nonempty  bool
		turn      string
		watermark uint64
		initial   rewind.InitialContinuation
		want      error
	}{
		{name: "original initial"},
		{name: "noninitial missing settlement", nonempty: true, want: rewind.ErrInvalid},
		{name: "initial with turn only", turn: "turn", want: rewind.ErrInvalid},
		{name: "watermark only", nonempty: true, watermark: 1, want: rewind.ErrInvalid},
		{name: "turn only", nonempty: true, turn: "turn", want: rewind.ErrInvalid},
		{name: "future watermark", nonempty: true, turn: "turn", watermark: 2, want: rewind.ErrInvalid},
		{name: "settled", nonempty: true, turn: "turn", watermark: 1},
		{name: "initial continuation", initial: initial},
		{name: "continuation with notice", nonempty: true, initial: initial},
		{name: "missing checkpoint", initial: rewind.InitialContinuation{CheckpointChecksum: "checksum"}, want: rewind.ErrInvalid},
		{name: "missing checksum", initial: rewind.InitialContinuation{CheckpointID: "origin"}, want: rewind.ErrInvalid},
		{name: "continuation with settlement", nonempty: true, initial: initial, turn: "turn", watermark: 1, want: rewind.ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder := transcript.NewBuilder()
			var messages []llm.Message
			if tt.nonempty {
				builder.AddNotice("", "arbitrary metadata, not settlement proof")
				messages = []llm.Message{{Role: llm.RoleUser, Content: "arbitrary metadata"}}
			}
			canonical, err := builder.Thread().CaptureCheckpoint()
			if err != nil {
				t.Fatal(err)
			}
			target := rewind.Target{Provider: "openai", Model: "model"}
			checkpoint, err := rewind.Capture(rewind.CaptureInput{
				ID: "checkpoint", SessionID: "session", Workspace: "/workspace", Transcript: canonical, Context: messages, Target: target,
				Boundary: rewind.Boundary{PromptID: "prompt", PromptText: "next", SettledTurnID: tt.turn, EventWatermark: tt.watermark, InitialContinuation: tt.initial},
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("capture = %v, want %v", err, tt.want)
			}
			if err == nil {
				record, err := checkpoint.Record()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := rewind.Decode(record); err != nil {
					t.Fatalf("decode capture: %v", err)
				}
			}
			data, err := rewind.EncodeResume(canonical.Revision(), messages, target, tt.turn, tt.watermark, tt.initial)
			if !errors.Is(err, tt.want) {
				t.Fatalf("encode resume = %v, want %v", err, tt.want)
			}
			if err == nil {
				if _, err := rewind.DecodeResume(data); err != nil {
					t.Fatalf("decode resume: %v", err)
				}
			} else {
				if messages == nil {
					messages = []llm.Message{}
				}
				data, err = json.Marshal(rewind.ResumeState{Version: 1, Revision: canonical.Revision(), Context: messages, Target: target, SettledTurnID: tt.turn, EventWatermark: tt.watermark, InitialContinuation: tt.initial})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := rewind.DecodeResume(data); !errors.Is(err, tt.want) {
					t.Fatalf("decode invalid resume = %v", err)
				}
			}
		})
	}
}
