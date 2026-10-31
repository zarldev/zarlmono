package runner_test

import (
	"context"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestAdditionalToolGateNarrowsWithoutChangingReplacementContract(t *testing.T) {
	denySecret := func(spec tools.ToolSpec) bool { return spec.Name != "secret" }
	readOnly := func(spec tools.ToolSpec) bool { return !spec.Mutates }
	cases := []struct {
		name  string
		scope func(context.Context) context.Context
		want  []string
	}{
		{"intersection", func(ctx context.Context) context.Context {
			return runner.WithAdditionalToolGate(runner.WithToolGate(ctx, denySecret), readOnly)
		}, []string{"read"}},
		{"nil preserves", func(ctx context.Context) context.Context {
			return runner.WithAdditionalToolGate(runner.WithToolGate(ctx, denySecret), nil)
		}, []string{"mutate", "read"}},
		{"first gate", func(ctx context.Context) context.Context { return runner.WithAdditionalToolGate(ctx, readOnly) }, []string{"read", "secret"}},
		{"replacement remains explicit", func(ctx context.Context) context.Context {
			return runner.WithToolGate(runner.WithToolGate(ctx, denySecret), readOnly)
		}, []string{"read", "secret"}},
		{"nil replacement preserves", func(ctx context.Context) context.Context {
			return runner.WithToolGate(runner.WithToolGate(ctx, denySecret), nil)
		}, []string{"mutate", "read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := &countingTool{name: "secret"}
			mutate := &countingTool{name: "mutate", mutates: true}
			read := &countingTool{name: "read"}
			provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{{chunkToolCall("secret", "secret", `{}`)}, {chunkToolCall("mutate", "mutate", `{}`)}, {chunkToolCall("read", "read", `{}`)}, {chunkText("done")}}}
			r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry(secret, mutate, read)))
			result := r.Run(tc.scope(t.Context()), runner.TaskSpec{ID: "gated", Prompt: "work"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			offered := provider.offeredOnFirstCall()
			slices.Sort(offered)
			if !slices.Equal(offered, tc.want) {
				t.Fatalf("offered=%v want=%v", offered, tc.want)
			}
			for _, tool := range []countingTool{*secret, *mutate, *read} {
				want := 0
				if slices.Contains(tc.want, string(tool.name)) {
					want = 1
				}
				if tool.calls != want {
					t.Errorf("%s calls=%d want=%d", tool.name, tool.calls, want)
				}
			}
		})
	}
}
