package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeHookFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write hook file: %v", err)
	}
	return path
}

func TestLoadHookFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Hook
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
			want: Hook{
				Name:        "gofmt-on-write",
				Description: "gofmt files after write",
				Event:       HookPostTool,
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
			want: Hook{
				Name:        "audit",
				Description: "log every tool call",
				Event:       HookPreTool,
				Timeout:     DefaultHookTimeout,
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
			path := writeHookFile(t, tt.content)
			got, err := loadHookFile(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadHookFile: %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadHookFile: %v", err)
			}
			tt.want.Source = path
			if got != tt.want {
				t.Errorf("loadHookFile = %+v, want %+v", got, tt.want)
			}
		})
	}
}
