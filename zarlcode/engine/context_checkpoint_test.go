package engine_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func exactCheckpoint(t *testing.T, messages []llm.Message) rewind.Checkpoint {
	t.Helper()
	builder := transcript.NewBuilder()
	boundary := rewind.Boundary{PromptID: "next", PromptText: "next"}
	if len(messages) != 0 {
		builder.AddUser(messages[0].Content)
		builder.AppendAssistant("settled", "", messages[1].Content)
		builder.FinishTurn("settled")
		boundary.SettledTurnID = "settled"
		boundary.EventWatermark = builder.Thread().Revision()
	}
	canonical, err := builder.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := rewind.Capture(rewind.CaptureInput{ID: "saved", SessionID: "source", Workspace: t.TempDir(), Boundary: boundary, Transcript: canonical, Context: messages, Target: rewind.Target{Provider: "openai-codex", Model: "saved-model"}})
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func TestContextCacheCheckpointExactDispatch(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "historical prompt"},
		{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(1), ContinuationItems: []llm.ContinuationItem{{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(0), Data: []byte("{ \"type\": \"reasoning\", \"id\": \"item\", \"encrypted_content\": \"opaque\" }\n")}}},
	}
	var cache engine.ContextCache
	if err := cache.RestoreCheckpoint(exactCheckpoint(t, history)); err != nil {
		t.Fatal(err)
	}
	cache.Run("edited prompt", func(spec runner.TaskSpec) runner.TaskResult {
		if !reflect.DeepEqual(spec.Context, history) {
			t.Fatal("exact context changed before dispatch")
		}
		spec.Context[1].ContinuationItems[0].Data[0] = '!'
		return runner.TaskResult{}
	})
	if !reflect.DeepEqual(cache.Snapshot(), history) {
		t.Fatal("dispatch escaped an alias")
	}
	if err := cache.RestoreCheckpoint(rewind.Checkpoint{}); !errors.Is(err, rewind.ErrUnavailable) {
		t.Fatalf("unavailable restore: %v", err)
	}
	if !reflect.DeepEqual(cache.Snapshot(), history) {
		t.Fatal("rejected restore changed context")
	}
}

func TestContextCacheCheckpointNeverRepairsLaterDispatch(t *testing.T) {
	t.Parallel()
	var cache engine.ContextCache
	if err := cache.RestoreCheckpoint(exactCheckpoint(t, nil)); err != nil {
		t.Fatal(err)
	}
	// A malformed result must remain observable to the next strict checkpoint
	// capture; exact dispatch must never silently truncate it into valid history.
	unpaired := []llm.Message{{Role: llm.RoleTool, ToolCallID: "missing", Content: "diagnostic"}}
	cache.Run("first", func(runner.TaskSpec) runner.TaskResult { return runner.TaskResult{Messages: unpaired} })
	cache.Run("second", func(spec runner.TaskSpec) runner.TaskResult {
		if !reflect.DeepEqual(spec.Context, unpaired) {
			t.Fatal("exact dispatch repaired history")
		}
		return runner.TaskResult{}
	})
	// Explicit legacy restore retains the old recovery contract.
	cache.Restore(unpaired)
	if len(cache.Snapshot()) != 0 {
		t.Fatal("legacy restore stopped repairing")
	}
}
