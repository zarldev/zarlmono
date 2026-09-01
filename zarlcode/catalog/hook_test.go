package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
)

func writeHookFile(ctx context.Context, t *testing.T, content string) (string, string) {
	t.Helper()
	if err := ctx.Err(); err != nil {
		t.Fatalf("test context: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, ".zarlcode", "hooks", "hook.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create hook directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write hook file: %v", err)
	}
	return root, path
}

func TestLoadHookFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    catalog.Hook
		wantErr string
	}{
		{
			name: "full frontmatter",
			content: `---
name: gofmt-on-write
description: gofmt files after write
event: post_tool
matcher: write|edit
timeout: 10s
blocking: true
---
gofmt -l .
`,
			want: catalog.Hook{
				Name:        "gofmt-on-write",
				Description: "gofmt files after write",
				Event:       catalog.HookPostTool,
				Matcher:     "write|edit",
				Timeout:     10 * time.Second,
				Blocking:    true,
				Command:     "gofmt -l .",
			},
		},
		{
			name: "defaults fill timeout and blocking",
			content: `---
name: audit
description: log every tool call
event: pre_tool
---
echo "$ZARLCODE_TOOL_NAME" >> .zarlcode-audit
`,
			want: catalog.Hook{
				Name:        "audit",
				Description: "log every tool call",
				Event:       catalog.HookPreTool,
				Timeout:     catalog.DefaultHookTimeout,
				Blocking:    false,
				Command:     `echo "$ZARLCODE_TOOL_NAME" >> .zarlcode-audit`,
			},
		},
		{
			name: "missing event",
			content: `---
name: x
description: y
---
exit 0
`,
			wantErr: "missing required field `event`",
		},
		{
			name: "unknown event",
			content: `---
name: x
description: y
event: on_save
---
exit 0
`,
			wantErr: "unknown event",
		},
		{
			name: "invalid matcher",
			content: `---
name: x
description: y
event: pre_tool
matcher: "write("
---
exit 0
`,
			wantErr: "compile matcher",
		},
		{
			name: "invalid timeout",
			content: `---
name: x
description: y
event: pre_tool
timeout: soonish
---
exit 0
`,
			wantErr: "parse timeout",
		},
		{
			name: "negative timeout",
			content: `---
name: x
description: y
event: pre_tool
timeout: -5s
---
exit 0
`,
			wantErr: "must be positive",
		},
		{
			name: "empty body",
			content: `---
name: x
description: y
event: pre_tool
---
`,
			wantErr: "hook body is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, path := writeHookFile(t.Context(), t, tt.content)
			hooks, errs := catalog.LoadHooks(root)
			if tt.wantErr != "" {
				if len(hooks) != 0 {
					t.Fatalf("LoadHooks returned %d hooks, want none", len(hooks))
				}
				if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.wantErr) {
					t.Fatalf("LoadHooks errors = %v, want one containing %q", errs, tt.wantErr)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("LoadHooks errors: %v", errs)
			}
			if len(hooks) != 1 {
				t.Fatalf("LoadHooks returned %d hooks, want 1", len(hooks))
			}
			tt.want.Source = path
			if hooks[0] != tt.want {
				t.Errorf("LoadHooks = %+v, want %+v", hooks[0], tt.want)
			}
		})
	}
}
