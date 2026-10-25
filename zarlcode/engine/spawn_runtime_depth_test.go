package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSpawnConstructionPreservesAbsoluteRuntimeDepth(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%v", named), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			args := `{"prompt":"descend one level","mode":"implement"}`
			if named {
				mustWrite(t, filepath.Join(root, ".zarlcode", "agents", "depth-fixture.md"), "---\nname: depth-fixture\ndescription: offline depth fixture\nmode: implement\n---\nFollow the offline fixture.\n")
				args = `{"prompt":"descend one level","mode":"implement","agent":"depth-fixture"}`
			}
			var mu sync.Mutex
			var requests [3]int
			provider := modeProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				depth := taskscope.DepthFrom(ctx)
				if depth > 2 {
					t.Errorf("execution exceeded absolute depth ceiling: %d", depth)
					return modeCalls()
				}
				mu.Lock()
				requests[depth]++
				step := requests[depth]
				mu.Unlock()
				switch step {
				case 1:
					if depth < 2 && !requestHasTool(req, "agent_spawn") {
						t.Errorf("spawn unavailable prematurely at depth %d", depth)
					}
					return modeCalls(namedModeCall("descend", "agent_spawn", args))
				case 2:
					result := modeResult(req.Messages, "descend")
					if depth == 2 {
						if !strings.Contains(result, "not available") && !strings.Contains(result, "max recursion depth 2") {
							t.Errorf("ceiling not enforced: %s", result)
						}
						return modeCalls()
					}
					if !strings.Contains(result, "task_id") {
						t.Errorf("spawn refused at depth %d: %s", depth, result)
						return modeCalls()
					}
					return modeCalls(namedModeCall("await", "agent_await", `{"timeout_seconds":30}`))
				default:
					return modeCalls()
				}
			})
			live := modeLive(t, root, provider)
			live.SetLimits(0, 5, 5, 2)
			if err := live.RunTurn(t.Context(), "offline two-level delegation"); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			got := requests
			mu.Unlock()
			if got != [3]int{3, 3, 2} {
				t.Fatalf("requests per depth = %v", got)
			}
		})
	}
}
