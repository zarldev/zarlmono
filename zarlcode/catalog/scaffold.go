package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// ErrExists is returned by the scaffold helpers when a file for the given
// name already exists, so the caller can offer to open it instead of
// clobbering it.
var ErrExists = errors.New("catalog: a definition with that name already exists")

// ScaffoldAgent writes a templated agent definition to the user config dir
// (~/.zarlcode/config/agents/<slug>.md) and returns its path. The template
// has valid frontmatter (so it loads immediately) with TODO placeholders for
// the body. Errors with ErrExists if the file is already there.
func ScaffoldAgent(name string) (string, error) {
	return scaffold("agents", name, agentTemplate(name))
}

// ScaffoldSkill writes a templated skill definition to the user config dir
// (~/.zarlcode/config/skills/<slug>.md) and returns its path.
func ScaffoldSkill(name string) (string, error) {
	return scaffold("skills", name, skillTemplate(name))
}

// ScaffoldHook writes a templated hook definition to the user config dir
// (~/.zarlcode/config/hooks/<slug>.md) and returns its path. The template is
// a working non-blocking post_tool hook, so loading it is safe before the
// user edits the TODOs.
func ScaffoldHook(name string) (string, error) {
	return scaffold("hooks", name, hookTemplate(name))
}

func scaffold(subdir, name, body string) (string, error) {
	slug := slugify(name)
	if slug == "" {
		return "", errors.New("catalog: name has no usable filename characters")
	}
	cfg, err := home.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	dir := filepath.Join(cfg, subdir)
	if err := os.MkdirAll(dir, filesystem.ModePublicDir); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(path); err == nil {
		return path, ErrExists
	}
	if err := os.WriteFile(path, []byte(body), filesystem.ModePublicFile); err != nil {
		return "", fmt.Errorf("write %q: %w", path, err)
	}
	return path, nil
}

func agentTemplate(name string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"description: TODO one-line summary of when to use this agent\n" +
		"# Optional overrides — delete any line to inherit the shell default:\n" +
		"# provider: claude\n" +
		"# model: \n" +
		"# max_iterations: 30\n" +
		"# thinking: true\n" +
		"# tool_output_format: labeled\n" +
		"---\n\n" +
		"You are " + name + ".\n\n" +
		"TODO write the system prompt: the agent's role, how it should work, and\nwhat a good final answer looks like.\n"
}

func skillTemplate(name string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"description: TODO when should the agent load this skill\n" +
		"---\n\n" +
		"TODO write the capability guide the agent reads when this skill's\ndescription matches the task at hand.\n"
}

func hookTemplate(name string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"description: TODO what this hook checks or does\n" +
		"# event: pre_tool fires before the tool runs; post_tool after.\n" +
		"event: post_tool\n" +
		"# matcher is a regexp matched against the whole tool name; delete to match every tool.\n" +
		"matcher: write|edit|apply_patch\n" +
		"# blocking: true rejects the tool call when this script exits non-zero.\n" +
		"blocking: false\n" +
		"# timeout: 30s\n" +
		"---\n\n" +
		"# The body is a shell script run via `sh -c` in the workspace root.\n" +
		"# Stdin carries a JSON payload: event, tool_name, arguments, and (post_tool)\n" +
		"# success + error. ZARLCODE_TOOL_NAME / ZARLCODE_HOOK_EVENT /\n" +
		"# ZARLCODE_WORKSPACE_ROOT are in the environment.\n" +
		"echo \"$ZARLCODE_TOOL_NAME ran\" >> .zarlcode-hook.log\n"
}

// slugify turns a display name into a safe lowercase filename stem: spaces and
// unsupported characters collapse to single hyphens, leading/trailing hyphens
// are trimmed.
func slugify(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
