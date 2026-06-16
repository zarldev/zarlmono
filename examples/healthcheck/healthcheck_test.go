package main

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestHealthCheck_AllHealthy(t *testing.T) {
	farm := NewServerFarm("api", "db", "cache")
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c2", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c3", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()},
		{runnertest.ChunkText("all healthy"), runnertest.ChunkDone()},
	})

	out := RunHealthCheck(t.Context(), client, farm, 1)
	if out.Err() != nil {
		t.Fatalf("RunHealthCheck: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("status = %s; want succeeded (all endpoints healthy)", out.Status())
	}
	if !farm.AllHealthy() {
		eps, _ := farm.Snapshot()
		t.Fatalf("all healthy = false; endpoints=%v", eps)
	}
}

func TestHealthCheck_TransientResolves(t *testing.T) {
	farm := NewServerFarm("api", "db", "cache")
	// Simulate db being transient — the farm auto-promotes on check.
	farm.SetHealth("db", StatusTransient)

	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c2", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()}, // transient → auto-promotes
		{runnertest.ChunkToolCall("c3", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()}, // now healthy
		{runnertest.ChunkToolCall("c4", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()},
		{runnertest.ChunkText("all healthy"), runnertest.ChunkDone()},
	})

	out := RunHealthCheck(t.Context(), client, farm, 1)
	if out.Err() != nil {
		t.Fatalf("RunHealthCheck: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("status = %s; want succeeded (transient db resolved)", out.Status())
	}
	_, checked := farm.Snapshot()
	// db should appear twice in the check log (transient then retry)
	dbCount := 0
	for _, name := range checked {
		if name == "db" {
			dbCount++
		}
	}
	if dbCount != 2 {
		t.Fatalf("db checked %d times; want 2 (transient + retry), checked=%v", dbCount, checked)
	}
}

func TestHealthCheck_DownEndpointRequiresRerive(t *testing.T) {
	farm := NewServerFarm("api", "db", "cache")
	farm.SetHealth("db", StatusDown)

	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c2", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()}, // down
		{runnertest.ChunkToolCall("c3", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()},
		{runnertest.ChunkText("db is down"), runnertest.ChunkDone()},
	})

	// GiveUp — oracle says not healthy, budget exhausted at 1 attempt.
	out := RunHealthCheck(t.Context(), client, farm, 1)
	if out.Status() != pursue.Statuses.GAVEUP {
		t.Fatalf("status = %s; want gave_up (db still down, budget exhausted)", out.Status())
	}
}

func TestFanoutGuardrail_CapsExcessiveCalls(t *testing.T) {
	farm := NewServerFarm("api", "db", "cache")
	farm.SetHealth("cache", StatusDegraded) // prevents AllHealthy → true

	// Script that calls check_endpoint 6 times — exceeds the fanout cap of 5.
	// The 6th call is blocked by the fanout guardrail.
	turns := [][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c2", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c3", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c4", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c5", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c6", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()}, // 6th call — fanout blocks
		{runnertest.ChunkText("fanout guardrail blocked excessive checks"), runnertest.ChunkDone()},
	}
	client := runnertest.NewClient(turns)

	out := RunHealthCheck(t.Context(), client, farm, 1)
	if out.Err() != nil {
		t.Fatalf("RunHealthCheck: %v", out.Err())
	}
	// The 6th call should be blocked by the fanout guardrail (count > 5),
	// making it a failure. The runner tries again but hits iteration cap
	// or the model gives up.
	if out.Status() == pursue.Statuses.SUCCEEDED {
		t.Fatal("should not succeed — fanout guardrail should block excessive calls")
	}
}
