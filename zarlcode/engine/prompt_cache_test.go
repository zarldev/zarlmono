package engine_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRecordedTurnsKeepSessionCacheRouteAndDistinctTaskIDs(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		name := "direct"
		if reserved {
			name = "reserved"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			store, err := db.Open(t.Context(), filepath.Join(root, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			for _, id := range []string{"session-a", "session-b"} {
				initializeControlSession(t, store, id)
			}
			type observed struct {
				task taskscope.ID
				key  string
			}
			var requests []observed
			provider := modeProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				key, _ := req.Options["prompt_cache_key"].(string)
				requests = append(requests, observed{task: taskscope.IDFrom(ctx), key: key})
				return modeCalls()
			})
			live := modeLive(t, root, provider)
			live.SetPlanMode(true)
			run := func(live *engine.LiveRunner, session string) {
				t.Helper()
				var err error
				if reserved {
					reservation, reserveErr := live.ReserveRuntime()
					if reserveErr != nil {
						t.Fatal(reserveErr)
					}
					err = reservation.RunRecordedTurn(t.Context(), "inspect only", nil, store, session)
					reservation.Release()
				} else {
					err = live.RunRecordedTurn(t.Context(), "inspect only", nil, store, session)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			run(live, "session-a")
			run(live, "session-a")
			resumed := modeLive(t, root, provider)
			resumed.SetPlanMode(true)
			run(resumed, "session-a")
			run(resumed, "session-b")
			if len(requests) != 4 {
				t.Fatalf("requests = %d, want 4", len(requests))
			}
			tasks := make(map[taskscope.ID]bool)
			for i, request := range requests {
				if request.task == "" || tasks[request.task] {
					t.Errorf("task identity reused: %q", request.task)
				}
				tasks[request.task] = true
				if len(request.key) != 64 || strings.Contains(request.key, "session-") || request.key == string(request.task) {
					t.Errorf("route is not an opaque session identity: %q", request.key)
				}
				if i < 3 && request.key != requests[0].key {
					t.Errorf("same/resumed session changed route at turn %d", i)
				}
			}
			if requests[3].key == requests[0].key {
				t.Error("new/fork session reused the parent route")
			}
		})
	}
}
