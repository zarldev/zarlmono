package tui

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// prTitleMax caps the rendered PR title so the workspace card stays a single
// readable line; the pane clips anything wider, but a hard cap keeps the state
// suffix visible.
const prTitleMax = 32

// prLine formats a PRInfo for the workspace card: "#123 Title · open", with the
// number in the accent colour and the state coloured by how it reads —
// merged/draft muted, closed warning, open success.
func prLine(pr *PRInfo) string {
	out := palette.Secondary.On("#"+itoa(pr.Number)) + " " +
		palette.Fg.On(truncateRunes(pr.Title, prTitleMax))

	state := strings.ToLower(pr.State)
	if pr.Draft {
		state = "draft"
	}
	tone := palette.Success
	switch state {
	case "merged", "draft":
		tone = palette.Muted
	case "closed":
		tone = palette.Warning
	}
	return out + palette.Muted.On(" · ") + tone.On(state)
}

// prFetchTimeout bounds the gh subprocess so a slow network can't stall the
// async lookup. The fetch runs off the Update loop, so this only caps how long
// we wait before giving up and leaving the PR line absent.
const prFetchTimeout = 5 * time.Second

// PRInfo is the at-a-glance view of the open GitHub PR for the active branch,
// rendered in the cockpit's workspace card. Populated asynchronously by
// fetchPRCmd; nil means "no PR, gh unavailable, or not yet resolved".
type PRInfo struct {
	Number int
	Title  string
	URL    string
	State  string // OPEN / MERGED / CLOSED, as reported by gh
	Draft  bool
}

// prLoadedMsg carries a completed PR lookup back to the Update loop. branch
// records which branch the lookup was for, so a result that lands after the
// branch changed can be discarded rather than shown against the wrong branch.
type prLoadedMsg struct {
	branch string
	pr     *PRInfo
}

// fetchPRCmd resolves the open GitHub PR for branch via the gh CLI, off the
// Update loop. Any failure (gh missing, no PR, not a GitHub repo, timeout)
// resolves to a nil PR rather than an error: the workspace card simply omits
// the PR line. dir scopes gh to the active workspace.
func fetchPRCmd(dir, branch string) tea.Cmd {
	if branch == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), prFetchTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "gh", "pr", "view", branch,
			"--json", "number,title,url,state,isDraft")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return prLoadedMsg{branch: branch, pr: nil}
		}

		var raw struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			State   string `json:"state"`
			IsDraft bool   `json:"isDraft"`
		}
		if err := json.Unmarshal(out, &raw); err != nil || raw.Number == 0 {
			return prLoadedMsg{branch: branch, pr: nil}
		}
		return prLoadedMsg{branch: branch, pr: &PRInfo{
			Number: raw.Number,
			Title:  raw.Title,
			URL:    raw.URL,
			State:  raw.State,
			Draft:  raw.IsDraft,
		}}
	}
}

// fetchPRCmd returns the lookup command for the session's active workspace and
// branch, or nil when there's no branch to resolve.
func (m *UI) fetchPRCmd() tea.Cmd {
	return fetchPRCmd(m.session.WorkspaceDir, m.session.Branch)
}

// handlePRMsg applies a completed PR lookup, ignoring stale results whose
// branch no longer matches the session's current branch.
func (m *UI) handlePRMsg(msg tea.Msg) bool {
	pm, ok := msg.(prLoadedMsg)
	if !ok {
		return false
	}
	if pm.branch == m.session.Branch {
		m.session.PR = pm.pr
	}
	return true
}

// notePRRelevantTool flags a re-fetch when a started tool plausibly changed the
// branch's PR state — a bash command invoking git or gh (push, pr create, …).
// The actual lookup is deferred to turn end (refreshPRCmd) so the gh subprocess
// fires once per turn, not per tool call.
func (m *UI) notePRRelevantTool(name string, params map[string]any) {
	switch strings.ToLower(name) {
	case "bash", "shell", "sh":
	default:
		return
	}
	cmd, _ := params["command"].(string)
	if cmd == "" {
		cmd, _ = params["cmd"].(string)
	}
	if gitCommandRe.MatchString(cmd) {
		m.prRefreshPending = true
	}
}

// gitCommandRe matches a git or gh invocation anywhere in a shell command
// (start of line or after a shell separator), so chained commands like
// "go test && git push" still trigger a refresh.
var gitCommandRe = regexp.MustCompile(`(^|[;&|]|&&|\|\|)\s*(git|gh)\s`)

// refreshPRCmd resolves the PR lookup to run at turn end: it re-reads the branch
// from disk (catching a checkout the agent performed) and re-fetches when the
// branch changed or a git/gh tool ran this turn. Returns nil when nothing
// warrants a lookup.
func (m *UI) refreshPRCmd() tea.Cmd {
	branchChanged := false
	if m.session.WorkspaceDir != "" {
		if b := gitBranch(m.session.WorkspaceDir); b != m.session.Branch {
			m.session.Branch = b
			m.session.PR = nil
			branchChanged = true
		}
	}
	if !branchChanged && !m.prRefreshPending {
		return nil
	}
	m.prRefreshPending = false
	return m.fetchPRCmd()
}
