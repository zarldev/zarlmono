// Package catalog discovers the on-disk agent, skill, and hook definitions a
// workspace exposes — markdown files with a YAML frontmatter header — and
// returns them as plain data. It is charm-free (stdlib + yaml + the home
// package only) so both the runtime and the v2 TUI can read the same
// inventory without dragging in a UI toolkit.
//
// Discovery mirrors the historical lookup order: per-user config first, then
// the canonical home, then the source tree, then the workspace-local dir
// (later directories win on a name collision, but first-seen order is kept so
// listings stay stable).
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

// Agent is one named agent profile. The execution fields (provider, model,
// iterations, thinking, mode, workspace) are optional in the frontmatter; an
// empty value means "inherit the shell default" unless documented otherwise.
type Agent struct {
	Name          string
	Description   string
	Provider      string
	Model         string
	MaxIterations int
	Thinking      bool
	Mode          string
	// Workspace is parsed from the agent definition but NOT yet enforced: a
	// delegated sub-agent runs in the parent's workspace, not this one. It
	// is intentionally not advertised in the system prompt or list_agents so
	// the model isn't told about isolation that doesn't exist. Honour it in
	// engine.buildAgentRunner (build a workspace-scoped tool source) before
	// re-advertising.
	Workspace        string
	ToolOutputFormat string
	Body             string // system prompt body
	Source           string // absolute path it was loaded from
}

// Skill is one capability guide: name + description metadata and the markdown
// body the agent reads when the description matches the task at hand.
type Skill struct {
	Name        string
	Description string
	Body        string
	Source      string
}

// HookEvent selects when a hook fires relative to tool dispatch.
type HookEvent string

const (
	// HookPreTool fires before the tool executes; a blocking hook that
	// exits non-zero converts the call into a failed result without
	// dispatching it.
	HookPreTool HookEvent = "pre_tool"
	// HookPostTool fires after the tool executes; a blocking hook that
	// exits non-zero replaces the result with a failure the model sees.
	HookPostTool HookEvent = "post_tool"
)

// DefaultHookTimeout bounds a hook command when the frontmatter doesn't
// set its own `timeout`.
const DefaultHookTimeout = 30 * time.Second

// Hook is one user-defined command hook: frontmatter selecting when it fires
// (event + tool matcher) plus the shell script body the engine executes. The
// engine arms discovered hooks as a guardrail in the production chain.
type Hook struct {
	Name        string
	Description string
	Event       HookEvent
	// Matcher is a regular expression matched against the whole tool name
	// (anchored, so `write|edit` means exactly those two tools). Empty
	// matches every tool. Validated at load time.
	Matcher string
	// Timeout bounds the hook command; DefaultHookTimeout when the
	// frontmatter omits it.
	Timeout time.Duration
	// Blocking decides what a non-zero exit means: true rejects the tool
	// call through the guardrail chain, false logs and lets it proceed.
	Blocking bool
	Command  string // body = the shell script run via `sh -c`
	Source   string
}

type agentFrontmatter struct {
	Name             string `yaml:"name"`
	Description      string `yaml:"description"`
	Provider         string `yaml:"provider"`
	Model            string `yaml:"model"`
	MaxIterations    int    `yaml:"max_iterations"`
	Thinking         *bool  `yaml:"thinking"`
	ToolOutputFormat string `yaml:"tool_output_format"`
	Mode             string `yaml:"mode"`
	Workspace        string `yaml:"workspace"`
	WorkspaceRoot    string `yaml:"workspace_root"`
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type hookFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Event       string `yaml:"event"`
	Matcher     string `yaml:"matcher"`
	Timeout     string `yaml:"timeout"`
	Blocking    bool   `yaml:"blocking"`
}

// LoadAgents walks the agent directories and returns the discovered agents in
// stable discovery order. Errors on individual files are collected, not fatal:
// one malformed agent shouldn't hide the rest.
func LoadAgents(workspaceRoot string) ([]Agent, []error) {
	var out []Agent
	idx := map[string]int{}
	var errs []error
	walkMarkdown(agentDirs(workspaceRoot), &errs, func(path string) error {
		a, err := loadAgentFile(path)
		if err != nil {
			return err
		}
		if i, ok := idx[a.Name]; ok {
			out[i] = a // later dir wins, position preserved
			return nil
		}
		idx[a.Name] = len(out)
		out = append(out, a)
		return nil
	})
	return out, errs
}

// LoadSkills walks the skill directories and returns the discovered skills in
// stable discovery order, with the same collected-error contract as LoadAgents.
func LoadSkills(workspaceRoot string) ([]Skill, []error) {
	var out []Skill
	idx := map[string]int{}
	var errs []error
	walkMarkdown(skillDirs(workspaceRoot), &errs, func(path string) error {
		s, err := loadSkillFile(path)
		if err != nil {
			return err
		}
		if i, ok := idx[s.Name]; ok {
			out[i] = s
			return nil
		}
		idx[s.Name] = len(out)
		out = append(out, s)
		return nil
	})
	return out, errs
}

// LoadHooks walks the hook directories and returns the discovered hooks in
// stable discovery order, with the same collected-error contract as LoadAgents.
func LoadHooks(workspaceRoot string) ([]Hook, []error) {
	var out []Hook
	idx := map[string]int{}
	var errs []error
	walkMarkdown(hookDirs(workspaceRoot), &errs, func(path string) error {
		h, err := loadHookFile(path)
		if err != nil {
			return err
		}
		if i, ok := idx[h.Name]; ok {
			out[i] = h
			return nil
		}
		idx[h.Name] = len(out)
		out = append(out, h)
		return nil
	})
	return out, errs
}

// walkMarkdown reads every *.md file under each dir (skipping absent dirs) and
// hands the path to load, collecting any returned error against the file.
func walkMarkdown(dirs []string, errs *[]error, load func(path string) error) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			*errs = append(*errs, fmt.Errorf("read dir %q: %w", dir, err))
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, ent.Name())
			if err := load(path); err != nil {
				*errs = append(*errs, fmt.Errorf("%q: %w", path, err))
			}
		}
	}
}

func agentDirs(workspaceRoot string) []string {
	var dirs []string
	if cfg, err := home.ConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "agents"))
	}
	dirs = append(dirs, filepath.Join(workspaceRoot, "zarlcode", "agents"))
	if ws := home.WorkspaceDir(workspaceRoot); ws != "" {
		dirs = append(dirs, filepath.Join(ws, "agents"))
	}
	return dirs
}

func skillDirs(workspaceRoot string) []string {
	var dirs []string
	if cfg, err := home.ConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "skills"))
	}
	if h, err := home.HomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, "skills"))
	}
	dirs = append(dirs, filepath.Join(workspaceRoot, "zarlcode", "skills"))
	if ws := home.WorkspaceDir(workspaceRoot); ws != "" {
		dirs = append(dirs, filepath.Join(ws, "skills"))
	}
	return dirs
}

func hookDirs(workspaceRoot string) []string {
	var dirs []string
	if cfg, err := home.ConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "hooks"))
	}
	if h, err := home.HomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, "hooks"))
	}
	dirs = append(dirs, filepath.Join(workspaceRoot, "zarlcode", "hooks"))
	if ws := home.WorkspaceDir(workspaceRoot); ws != "" {
		dirs = append(dirs, filepath.Join(ws, "hooks"))
	}
	return dirs
}

func loadAgentFile(path string) (Agent, error) {
	front, body, err := readFrontmatter(path)
	if err != nil {
		return Agent{}, err
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Agent{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Name == "" {
		return Agent{}, errors.New("frontmatter is missing required field `name`")
	}
	if fm.Description == "" {
		return Agent{}, errors.New("frontmatter is missing required field `description`")
	}
	mode, err := parseAgentMode(fm.Mode)
	if err != nil {
		return Agent{}, err
	}
	thinking := false
	if fm.Thinking != nil {
		thinking = *fm.Thinking
	}
	return Agent{
		Name:             fm.Name,
		Description:      fm.Description,
		Provider:         strings.TrimSpace(fm.Provider),
		Model:            strings.TrimSpace(fm.Model),
		MaxIterations:    fm.MaxIterations,
		Thinking:         thinking,
		Mode:             mode,
		Workspace:        firstNonEmpty(strings.TrimSpace(fm.WorkspaceRoot), strings.TrimSpace(fm.Workspace)),
		ToolOutputFormat: strings.TrimSpace(fm.ToolOutputFormat),
		Body:             strings.TrimSpace(body),
		Source:           path,
	}, nil
}

func parseAgentMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "explore", "verify", "implement":
		return mode, nil
	default:
		return "", fmt.Errorf("frontmatter mode %q must be one of explore, verify, implement", raw)
	}
}

func loadSkillFile(path string) (Skill, error) {
	front, body, err := readFrontmatter(path)
	if err != nil {
		return Skill{}, err
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Skill{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Name == "" {
		return Skill{}, errors.New("frontmatter is missing required field `name`")
	}
	if fm.Description == "" {
		return Skill{}, errors.New("frontmatter is missing required field `description`")
	}
	return Skill{
		Name:        fm.Name,
		Description: fm.Description,
		Body:        strings.TrimSpace(body),
		Source:      path,
	}, nil
}

func loadHookFile(path string) (Hook, error) {
	front, body, err := readFrontmatter(path)
	if err != nil {
		return Hook{}, err
	}
	var fm hookFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Hook{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Name == "" {
		return Hook{}, errors.New("frontmatter is missing required field `name`")
	}
	if fm.Description == "" {
		return Hook{}, errors.New("frontmatter is missing required field `description`")
	}
	event := HookEvent(strings.TrimSpace(fm.Event))
	switch event {
	case HookPreTool, HookPostTool:
	case "":
		return Hook{}, errors.New("frontmatter is missing required field `event`")
	default:
		return Hook{}, fmt.Errorf("unknown event %q (want %q or %q)", event, HookPreTool, HookPostTool)
	}
	matcher := strings.TrimSpace(fm.Matcher)
	if matcher != "" {
		if _, err := regexp.Compile(matcher); err != nil {
			return Hook{}, fmt.Errorf("compile matcher %q: %w", matcher, err)
		}
	}
	timeout := DefaultHookTimeout
	if t := strings.TrimSpace(fm.Timeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return Hook{}, fmt.Errorf("parse timeout %q: %w", t, err)
		}
		if d <= 0 {
			return Hook{}, fmt.Errorf("timeout %q must be positive", t)
		}
		timeout = d
	}
	command := strings.TrimSpace(body)
	if command == "" {
		return Hook{}, errors.New("hook body is empty — the body is the shell script to run")
	}
	return Hook{
		Name:        fm.Name,
		Description: fm.Description,
		Event:       event,
		Matcher:     matcher,
		Timeout:     timeout,
		Blocking:    fm.Blocking,
		Command:     command,
		Source:      path,
	}, nil
}

func readFrontmatter(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return splitFrontmatter(data)
}

// splitFrontmatter pulls the YAML block between leading "---" lines off the top
// of a markdown file and returns (front, body). It tolerates CRLF and a missing
// trailing newline after the closing delimiter (common in LLM-emitted files).
func splitFrontmatter(data []byte) ([]byte, string, error) {
	const delim = "---\n"
	s := string(data)
	if !strings.HasPrefix(s, delim) && !strings.HasPrefix(s, "---\r\n") {
		return nil, "", errors.New("missing leading `---` frontmatter delimiter")
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(s, delim), "---\r\n")
	for _, closer := range []string{"\n---\n", "\n---\r\n"} {
		if idx := strings.Index(rest, closer); idx >= 0 {
			front := []byte(rest[:idx])
			body := strings.TrimPrefix(rest[idx:], closer[:1])
			body = strings.TrimPrefix(body, "---\n")
			body = strings.TrimPrefix(body, "---\r\n")
			return front, body, nil
		}
	}
	if before, ok := strings.CutSuffix(rest, "\n---"); ok {
		return []byte(before), "", nil
	}
	return nil, "", errors.New("missing closing `---` frontmatter delimiter")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
