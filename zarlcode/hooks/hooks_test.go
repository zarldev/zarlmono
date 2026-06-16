package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/hooks"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func hook(name string, event catalog.HookEvent, matcher, command string, blocking bool) catalog.Hook {
	return catalog.Hook{
		Name:        name,
		Description: name,
		Event:       event,
		Matcher:     matcher,
		Timeout:     catalog.DefaultHookTimeout,
		Blocking:    blocking,
		Command:     command,
	}
}

func call(tool string) tools.ToolCall {
	return tools.ToolCall{
		ID:        "call-1",
		ToolName:  tools.ToolName(tool),
		Arguments: tools.ToolParameters{"path": "main.go"},
	}
}

func TestGuardrailBefore(t *testing.T) {
	tests := []struct {
		name    string
		hooks   []catalog.Hook
		tool    string
		wantErr string
	}{
		{
			name:  "passing hook admits the call",
			hooks: []catalog.Hook{hook("ok", catalog.HookPreTool, "", "exit 0", true)},
			tool:  "write",
		},
		{
			name:    "blocking hook failure rejects with its output",
			hooks:   []catalog.Hook{hook("deny", catalog.HookPreTool, "", "echo no writes today; exit 1", true)},
			tool:    "write",
			wantErr: "no writes today",
		},
		{
			name:  "non-blocking hook failure is ignored",
			hooks: []catalog.Hook{hook("advisory", catalog.HookPreTool, "", "exit 1", false)},
			tool:  "write",
		},
		{
			name:  "matcher skips non-matching tools",
			hooks: []catalog.Hook{hook("deny-write", catalog.HookPreTool, "write|edit", "exit 1", true)},
			tool:  "read",
		},
		{
			name:    "matcher catches matching tools",
			hooks:   []catalog.Hook{hook("deny-write", catalog.HookPreTool, "write|edit", "exit 1", true)},
			tool:    "edit",
			wantErr: `hook "deny-write"`,
		},
		{
			name:  "matcher is anchored to the whole name",
			hooks: []catalog.Hook{hook("deny-write", catalog.HookPreTool, "write", "exit 1", true)},
			tool:  "write_append",
		},
		{
			name: "first blocking failure short-circuits in order",
			hooks: []catalog.Hook{
				hook("first", catalog.HookPreTool, "", "echo first; exit 1", true),
				hook("second", catalog.HookPreTool, "", "echo second; exit 1", true),
			},
			tool:    "write",
			wantErr: `hook "first"`,
		},
		{
			name:  "post hooks do not fire on Before",
			hooks: []catalog.Hook{hook("post-only", catalog.HookPostTool, "", "exit 1", true)},
			tool:  "write",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := hooks.NewGuardrail(t.TempDir(), tt.hooks)
			if err != nil {
				t.Fatalf("NewGuardrail: %v", err)
			}
			err = g.Before(t.Context(), call(tt.tool))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Before: %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Before: %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestGuardrailBeforePayload(t *testing.T) {
	root := t.TempDir()
	g, err := hooks.NewGuardrail(root, []catalog.Hook{
		hook("capture", catalog.HookPreTool, "", "cat > captured.json", true),
	})
	if err != nil {
		t.Fatalf("NewGuardrail: %v", err)
	}
	if err := g.Before(t.Context(), call("write")); err != nil {
		t.Fatalf("Before: %v", err)
	}

	// The hook runs in the workspace root, so the relative path lands there.
	data, err := os.ReadFile(filepath.Join(root, "captured.json"))
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	var p struct {
		Event         string         `json:"event"`
		WorkspaceRoot string         `json:"workspace_root"`
		ToolName      string         `json:"tool_name"`
		ToolID        string         `json:"tool_id"`
		Arguments     map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode captured payload: %v", err)
	}
	if p.Event != "pre_tool" || p.ToolName != "write" || p.ToolID != "call-1" {
		t.Errorf("payload header = %+v, want pre_tool/write/call-1", p)
	}
	if p.WorkspaceRoot != root {
		t.Errorf("workspace_root = %q, want %q", p.WorkspaceRoot, root)
	}
	if p.Arguments["path"] != "main.go" {
		t.Errorf("arguments = %v, want path=main.go", p.Arguments)
	}
}

func TestGuardrailInspect(t *testing.T) {
	failed := &tools.ToolResult{ToolCallID: "call-1", Success: false, Error: "boom"}
	ok := &tools.ToolResult{ToolCallID: "call-1", Success: true}

	t.Run("post hook sees the outcome", func(t *testing.T) {
		root := t.TempDir()
		g, err := hooks.NewGuardrail(root, []catalog.Hook{
			hook("capture", catalog.HookPostTool, "", "cat > captured.json", true),
		})
		if err != nil {
			t.Fatalf("NewGuardrail: %v", err)
		}
		if err := g.Inspect(t.Context(), call("write"), failed, nil); err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(root, "captured.json"))
		if err != nil {
			t.Fatalf("read captured payload: %v", err)
		}
		var p struct {
			Event   string `json:"event"`
			Success *bool  `json:"success"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatalf("decode captured payload: %v", err)
		}
		if p.Event != "post_tool" || p.Success == nil || *p.Success || p.Error != "boom" {
			t.Errorf("payload = %+v, want post_tool/success=false/error=boom", p)
		}
	})

	t.Run("blocking post hook failure replaces the result", func(t *testing.T) {
		g, err := hooks.NewGuardrail(t.TempDir(), []catalog.Hook{
			hook("verify", catalog.HookPostTool, "", "echo lint failed; exit 1", true),
		})
		if err != nil {
			t.Fatalf("NewGuardrail: %v", err)
		}
		err = g.Inspect(t.Context(), call("write"), ok, nil)
		if err == nil || !strings.Contains(err.Error(), "lint failed") {
			t.Fatalf("Inspect: %v, want error containing %q", err, "lint failed")
		}
	})
}

func TestGuardrailTimeout(t *testing.T) {
	h := hook("slow", catalog.HookPreTool, "", "sleep 5", true)
	h.Timeout = 100 * time.Millisecond
	g, err := hooks.NewGuardrail(t.TempDir(), []catalog.Hook{h})
	if err != nil {
		t.Fatalf("NewGuardrail: %v", err)
	}
	start := time.Now()
	err = g.Before(t.Context(), call("write"))
	if err == nil {
		t.Fatal("Before: nil, want timeout rejection")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Before took %v, want the 100ms timeout to cut the 5s sleep", elapsed)
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Errorf("Before: %v, want deadline exceeded", err)
	}
}

func TestGuardrailEmpty(t *testing.T) {
	g, err := hooks.NewGuardrail(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewGuardrail: %v", err)
	}
	if !g.Empty() {
		t.Error("Empty() = false for a guardrail with no hooks")
	}
	g, err = hooks.NewGuardrail(t.TempDir(), []catalog.Hook{
		hook("ok", catalog.HookPreTool, "", "exit 0", true),
	})
	if err != nil {
		t.Fatalf("NewGuardrail: %v", err)
	}
	if g.Empty() {
		t.Error("Empty() = true for a guardrail with one hook")
	}
}

func TestNewGuardrailRejectsBadMatcher(t *testing.T) {
	_, err := hooks.NewGuardrail(t.TempDir(), []catalog.Hook{
		hook("bad", catalog.HookPreTool, "write(", "exit 0", true),
	})
	if err == nil || !strings.Contains(err.Error(), "compile matcher") {
		t.Fatalf("NewGuardrail: %v, want compile matcher error", err)
	}
}
