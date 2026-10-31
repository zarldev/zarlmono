package spawn_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestMixedReceivingScopesRetainOnlyPendingEvidence(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(1))
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	for _, id := range []taskscope.ID{"automatic", "explicit"} {
		if _, err := group.Bind(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	group.Ready("automatic")
	tool := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group)
	for range 5 {
		for _, parent := range []taskscope.ID{"automatic", "explicit"} {
			ctx := taskscope.WithID(t.Context(), parent)
			id := startScopedTask(t, ctx, tool)
			if _, err := group.Await(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := len(group.List()); got != 6 {
		t.Fatalf("retained=%d want five pending plus one observed", got)
	}
	ready := group.Ready("automatic")
	if len(ready.Inputs) != 5 {
		t.Fatalf("pending=%d want five", len(ready.Inputs))
	}
	var refs []tools.AdmissionReference
	for _, input := range ready.Inputs {
		refs = append(refs, input.Reference)
	}
	group.Admit("automatic", refs)
	if got := len(group.List()); got != 1 {
		t.Fatalf("post-admission retention=%d", got)
	}
}

func TestPreparationFailureReleasesCapacityAndWakesParent(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxConcurrent(1))
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	ctx := taskscope.WithID(t.Context(), "parent")
	if _, err := group.Bind(ctx, "parent"); err != nil {
		t.Fatal(err)
	}
	before := group.Ready("parent")
	broken := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group, spawn.WithFallbackPolicy(spawn.FallbackError))
	result, err := broken.Execute(ctx, tools.ToolCall{ID: "failure", Arguments: tools.ToolParameters{"prompt": "work", "agent": "unknown"}})
	if err != nil || result.Success {
		t.Fatalf("prepare failure=%v, %v", result, err)
	}
	select {
	case <-before.Changed:
	default:
		t.Fatal("failure did not wake parent")
	}
	if group.Ready("parent").Outstanding {
		t.Fatal("failed preparation remains outstanding")
	}
	id := startScopedTask(t, ctx, spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group))
	if _, err := group.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	ready := group.Ready("parent")
	if len(ready.Inputs) != 1 {
		t.Fatalf("promoted child missing: %#v", ready)
	}
	group.Admit("parent", []tools.AdmissionReference{ready.Inputs[0].Reference})
	if !group.Finish("parent") {
		t.Fatal("preparation slot leaked after promotion")
	}
}

func TestClosedReceiverDoesNotPinObservedResults(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(1))
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	ctx := taskscope.WithID(t.Context(), "parent")
	scope, err := group.Bind(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	group.Ready("parent")
	for range 5 {
		id := startScopedTask(t, ctx, spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group))
		if _, err := group.Await(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(group.List()); got != 1 {
		t.Fatalf("closed receiver pins %d observed results", got)
	}
}
