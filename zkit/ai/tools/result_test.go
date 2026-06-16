package tools_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestSuccessBuildsToolResult(t *testing.T) {
	t.Parallel()

	effect := tools.NewFileEffect(tools.FileCreate, "README.md")
	before := time.Now()
	res := tools.Success("call-1", "ok", effect)
	after := time.Now()

	if res.ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %q, want call-1", res.ToolCallID)
	}
	if !res.Success {
		t.Fatal("Success = false, want true")
	}
	if res.Data != "ok" {
		t.Fatalf("Data = %v, want ok", res.Data)
	}
	if len(res.Effects) != 1 || res.Effects[0].Kind != tools.EffectFile {
		t.Fatalf("Effects = %#v, want one file effect", res.Effects)
	}
	if res.ExecutedAt.Before(before) || res.ExecutedAt.After(after) {
		t.Fatalf("ExecutedAt = %s, want between %s and %s", res.ExecutedAt, before, after)
	}
}
