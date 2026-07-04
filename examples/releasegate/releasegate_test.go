package main

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestReleaseGate_DeterministicEndToEnd(t *testing.T) {
	rel := NewRelease("v1.2.3")
	client := runnertest.NewClient(defaultScript())

	out := RunReleaseGate(t.Context(), client, rel, 1)
	if out.Err() != nil {
		t.Fatalf("RunReleaseGate: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("status = %s; want succeeded", out.Status())
	}

	s := rel.Snapshot()
	if !s.Published || s.Channel != "production" {
		t.Fatalf("published=%t channel=%q; want production publish", s.Published, s.Channel)
	}
	if !s.NotesApproved {
		t.Fatal("notes should be approved by the post-call guardrail")
	}
	if len(s.Missing) != 0 {
		t.Fatalf("missing = %v; want empty gate", s.Missing)
	}
}

func TestReleaseReadyGuardrail_BlocksEarlyPublish(t *testing.T) {
	rel := NewRelease("v1.2.3")
	source := guardedSource(rel)

	res, err := source.Execute(t.Context(), tools.ToolCall{
		ID:        "publish-early",
		ToolName:  ToolPublish,
		Arguments: tools.ToolParameters{"channel": "production"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("publish should be blocked while the gate is missing checks")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("err = %v; want validation", res.Err)
	}
	if !strings.Contains(res.Error, "release_ready") || !strings.Contains(res.Error, "tests") {
		t.Fatalf("error = %q; want actionable release_ready rejection", res.Error)
	}
}

func TestSchemaGuardrail_RejectsMissingRequiredField(t *testing.T) {
	rel := NewRelease("v1.2.3")
	source := guardedSource(rel)

	res, err := source.Execute(t.Context(), tools.ToolCall{
		ID:        "bad-check",
		ToolName:  ToolSetCheck,
		Arguments: tools.ToolParameters{"name": "tests", "ok": true},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("schema should reject missing evidence before the tool executes")
	}
	if !strings.Contains(res.Error, "missing required field") {
		t.Fatalf("error = %q; want missing field feedback", res.Error)
	}
}

func TestNotesQualityGuardrail_RewritesWeakNotes(t *testing.T) {
	rel := NewRelease("v1.2.3")
	source := guardedSource(rel)

	res, err := source.Execute(t.Context(), tools.ToolCall{
		ID:       "weak-notes",
		ToolName: ToolWriteNotes,
		Arguments: tools.ToolParameters{
			"summary":  "ok",
			"risk":     "low",
			"rollback": "revert",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("weak notes should be rewritten into a failed tool result")
	}
	if rel.Snapshot().NotesApproved {
		t.Fatal("weak notes should not be approved")
	}

	res, err = source.Execute(t.Context(), tools.ToolCall{
		ID:       "good-notes",
		ToolName: ToolWriteNotes,
		Arguments: tools.ToolParameters{
			"summary":  "Adds a deterministic release gate example.",
			"risk":     "Low risk because it is docs only.",
			"rollback": "Revert the example directory if needed.",
		},
	})
	if err != nil {
		t.Fatalf("Execute good notes: %v", err)
	}
	if !res.Success {
		t.Fatalf("good notes rejected: %s", res.Error)
	}
	if !rel.Snapshot().NotesApproved {
		t.Fatal("good notes should be approved")
	}
}

func guardedSource(rel *Release) tools.Source {
	reg := tools.NewRegistry()
	reg.Register(statusTool{r: rel})
	reg.Register(newSetCheckTool(rel))
	reg.Register(newWriteNotesTool(rel))
	reg.Register(newPublishTool(rel))
	return guardrails.NewGuardedSource(reg, guardrails.NewSchemaGuardrail(reg),
		releaseReadyGuardrail{r: rel},
		notesQualityGuardrail{r: rel},
	)
}
