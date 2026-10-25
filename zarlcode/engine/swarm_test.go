package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSwarmSharesBeforeAuthorCompletesAndReactivatesIdlePeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		publish, finishWriter := make(chan struct{}), make(chan struct{})
		var writerCalls atomic.Int32
		var bodyRead atomic.Bool
		writer := swarmRuntime(t, root, swarmProvider{complete: func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				switch writerCalls.Add(1) {
				case 1:
					if !swarmHasTool(req, "board_post") {
						t.Error("read-only member cannot share findings")
					}
					select {
					case <-publish:
						yield(runnertest.ChunkToolCall("post", "board_post", `{"summary":"useful finding","body":"full retained evidence"}`), nil)
					case <-ctx.Done():
						yield(llm.CompletionChunk{}, ctx.Err())
					}
				default:
					select {
					case <-finishWriter:
						yield(runnertest.ChunkText("Research complete; no file changes requested."), nil)
					case <-ctx.Done():
						yield(llm.CompletionChunk{}, ctx.Err())
					}
				}
			}
		}})
		reader := swarmRuntime(t, root, swarmProvider{complete: func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				notice, pulled := false, false
				for _, message := range req.Messages {
					notice = notice || strings.Contains(message.Content, `"kind":"board_notice"`)
					pulled = pulled || (message.Role == llm.RoleTool && strings.Contains(message.Content, "full retained evidence"))
				}
				if notice && !pulled {
					yield(runnertest.ChunkToolCall("read", "board_read", `{"ids":[1]}`), nil)
					return
				}
				if pulled {
					bodyRead.Store(true)
				}
				yield(runnertest.ChunkText("Information reviewed; no changes requested."), nil)
			}
		}})
		goal := engine.NewSwarm("shared research")
		if _, err := goal.Add("writer", writer, "Research and share findings. Do not edit files."); err != nil {
			t.Fatal(err)
		}
		if _, err := goal.Add("reader", reader, "Review useful peer findings. Do not edit files."); err != nil {
			t.Fatal(err)
		}
		startEngineSwarm(t, goal)
		synctest.Wait()
		initial, _ := goal.Snapshot("reader")
		if initial.State != swarm.States.IDLE || initial.Activations != 1 {
			t.Fatalf("reader did not become idle: %+v", initial)
		}
		close(publish)
		synctest.Wait()
		peer, _ := goal.Snapshot("reader")
		author, _ := goal.Snapshot("writer")
		if peer.State != swarm.States.IDLE || peer.Activations != 2 || author.State != swarm.States.RUNNING || !bodyRead.Load() {
			t.Fatalf("peer sharing before completion: reader=%+v writer=%+v pulled=%v", peer, author, bodyRead.Load())
		}
		close(finishWriter)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := goal.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		// Finishing one member must not close the coordinator needed on later turns.
		draft, _ := board.ParseDraft("follow-up", "new host evidence", "", 0)
		if _, err := goal.Post(ctx, draft); err != nil {
			t.Fatal(err)
		}
		if err := goal.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range []swarm.AgentID{"writer", "reader"} {
			snapshot, _ := goal.Snapshot(id)
			if snapshot.State != swarm.States.IDLE || snapshot.Pending != 0 || snapshot.LastError != nil {
				t.Fatalf("follow-up %s: %+v", id, snapshot)
			}
		}
	})
}

func TestSwarmRejectsDuplicateRuntimesAndDifferentWorkspaces(t *testing.T) {
	provider := swarmProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream { return nil }}
	live := swarmRuntime(t, t.TempDir(), provider)
	other := swarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("scope")
	defer func() { _ = goal.Close(t.Context()) }()
	if _, err := goal.Add("a", live, "inspect"); err != nil {
		t.Fatal(err)
	}
	if _, err := goal.Add("b", live, "inspect"); !errors.Is(err, swarm.ErrConflict) {
		t.Fatalf("same runtime admitted twice: %v", err)
	}
	if _, err := goal.Add("b", other, "inspect"); !errors.Is(err, engine.ErrSwarmWorkspace) {
		t.Fatalf("foreign workspace admitted: %v", err)
	}
}

type swarmProvider struct {
	complete func(context.Context, llm.CompletionRequest) llm.CompletionStream
}

func (p swarmProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return p.complete(ctx, req)
}

func (swarmProvider) Name() string { return "openai" }

func swarmRuntime(t *testing.T, root string, provider llm.Provider) *engine.LiveRunner {
	t.Helper()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	wire := openai.NewProvider("fixture-key", openai.WithHTTPClient(&http.Client{Transport: swarmTransport{provider: provider}}))
	live := engine.NewLiveRunner(wire, workspace, "fixture-model", engine.WithReadOnlyTasks())
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		if err := workspace.Close(); err != nil {
			t.Error(err)
		}
	})
	return live
}

func startEngineSwarm(t *testing.T, goal *engine.Swarm) {
	t.Helper()
	ctx := t.Context()
	done := make(chan error, 1)
	go func() { done <- goal.Run(ctx) }()
	t.Cleanup(func() {
		if err := goal.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, swarm.ErrClosed) && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
}

func swarmHasTool(req llm.CompletionRequest, name string) bool {
	for _, tool := range req.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// Exercise a supported provider's actual wire projection without external I/O.
type swarmTransport struct{ provider llm.Provider }

func (s swarmTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	var req llm.CompletionRequest
	if err := json.NewDecoder(request.Body).Decode(&req); err != nil {
		return nil, err
	}
	var body strings.Builder
	for chunk, err := range s.provider.Complete(request.Context(), req) {
		if err != nil {
			return nil, err
		}
		delta := map[string]any{"content": chunk.Content}
		finish := "stop"
		if len(chunk.ToolCalls) > 0 {
			var calls []map[string]any
			for index, call := range chunk.ToolCalls {
				calls = append(calls, map[string]any{"index": index, "id": call.ID, "type": "function", "function": call.Function})
			}
			delta["tool_calls"] = calls
			finish = "tool_calls"
		}
		data, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "model": "fixture-model",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(&body, "data: %s\n\n", data)
	}
	body.WriteString("data: [DONE]\n\n")
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(body.String())), Request: request}, nil
}
