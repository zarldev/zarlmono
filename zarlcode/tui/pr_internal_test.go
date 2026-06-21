package tui

import (
	"strings"
	"testing"
)

func TestPRLine(t *testing.T) {
	tests := []struct {
		name string
		pr   *PRInfo
		want []string // substrings expected in the rendered line
	}{
		{
			name: "open",
			pr:   &PRInfo{Number: 42, Title: "Add the thing", State: "OPEN"},
			want: []string{"#42", "Add the thing", "open"},
		},
		{
			name: "draft overrides state",
			pr:   &PRInfo{Number: 7, Title: "WIP", State: "OPEN", Draft: true},
			want: []string{"#7", "draft"},
		},
		{
			name: "merged",
			pr:   &PRInfo{Number: 3, Title: "Done", State: "MERGED"},
			want: []string{"#3", "merged"},
		},
		{
			name: "long title truncated",
			pr:   &PRInfo{Number: 1, Title: strings.Repeat("x", 100), State: "OPEN"},
			want: []string{"…"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prLine(tt.pr)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("prLine() = %q, want substring %q", got, w)
				}
			}
		})
	}
}

func TestNotePRRelevantTool(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		command string
		want    bool
	}{
		{"git push", "bash", "git push -u origin HEAD", true},
		{"gh pr create", "bash", "gh pr create --fill", true},
		{"chained git", "bash", "go test ./... && git push", true},
		{"piped gh", "shell", "echo x | gh pr create", true},
		{"non-git bash", "bash", "go build ./...", false},
		{"substring not command", "bash", "echo digit && echo github", false},
		{"non-bash tool", "edit", "git push", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.notePRRelevantTool(tt.tool, map[string]any{"command": tt.command})
			if m.prRefreshPending != tt.want {
				t.Errorf("prRefreshPending = %v, want %v", m.prRefreshPending, tt.want)
			}
		})
	}
}

func TestRefreshPRCmd(t *testing.T) {
	t.Run("no branch change, no pending tool returns nil", func(t *testing.T) {
		m := New()
		m.session.Branch = "main"
		if cmd := m.refreshPRCmd(); cmd != nil {
			t.Error("refreshPRCmd should return nil when nothing changed")
		}
	})

	t.Run("pending git tool triggers fetch and clears flag", func(t *testing.T) {
		m := New()
		m.session.Branch = "main"
		m.prRefreshPending = true
		if cmd := m.refreshPRCmd(); cmd == nil {
			t.Error("refreshPRCmd should return a fetch command when a git tool ran")
		}
		if m.prRefreshPending {
			t.Error("prRefreshPending should be cleared after refresh")
		}
	})
}

func TestHandlePRMsgStaleBranchIgnored(t *testing.T) {
	m := New()
	m.session.Branch = "feat/current"

	// A result for a branch the session has since moved off of is discarded.
	stale := prLoadedMsg{branch: "feat/old", pr: &PRInfo{Number: 1, State: "OPEN"}}
	if !m.handlePRMsg(stale) {
		t.Fatal("handlePRMsg should report it handled a prLoadedMsg")
	}
	if m.session.PR != nil {
		t.Errorf("stale PR result applied: got %+v, want nil", m.session.PR)
	}

	// A result for the active branch lands.
	fresh := prLoadedMsg{branch: "feat/current", pr: &PRInfo{Number: 9, State: "OPEN"}}
	m.handlePRMsg(fresh)
	if m.session.PR == nil || m.session.PR.Number != 9 {
		t.Errorf("fresh PR result not applied: got %+v", m.session.PR)
	}
}
