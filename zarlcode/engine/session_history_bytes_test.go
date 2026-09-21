package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
)

type byteHistoryProvider struct{ client *runnertest.Client }

func (p byteHistoryProvider) Name() string { return "openai" }
func (p byteHistoryProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return p.client.Complete(ctx, request)
}

func TestRecordedTurnPreservesNonUTF8ToolBytes(t *testing.T) {
	const raw = "f\xffg"
	const args = "{\"query\":\"f\xffg\"}"
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: "session", Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), "session", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{SessionID: "session", ID: "initial", SourceSessionID: "session", Workspace: "/workspace", BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref}); err != nil {
		t.Fatal(err)
	}
	live := reservationRunner(t, byteHistoryProvider{client: runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("call", "web_search", args)}, {runnertest.ChunkText("done")},
	})})
	live.SetWebSearch(tools.New(tools.ToolSpec{Name: "web_search", Description: "returns bytes"}, func(context.Context, map[string]any) (string, error) { return raw, nil }))
	if err := live.RunRecordedTurn(t.Context(), "search", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionHistory(t.Context(), "session", live.RecordedHistory("session")); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReadSessionReplay(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, data := range replay {
		record, err := runner.UnmarshalReplayMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		if record.Tool != nil {
			found = true
			if record.Tool.Args != args || record.Tool.Output != raw || record.Message.Content != raw {
				t.Fatalf("raw bytes changed: args=%x output=%x content=%x", record.Tool.Args, record.Tool.Output, record.Message.Content)
			}
		}
	}
	if !found {
		t.Fatal("missing tool occurrence")
	}
	model, err := store.GetSessionModelContext(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(model.RequestJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	request, err := runner.UnmarshalHistoryRequest(envelope.Request)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, message := range request.Messages {
		if message.Role == llm.RoleTool {
			found = true
			if message.Content != raw {
				t.Fatalf("prepared bytes changed: %x", message.Content)
			}
		}
	}
	if !found {
		t.Fatal("prepared request lost tool result")
	}
}
