// Package hooks arms user-defined command hooks as a guardrail in the
// production tool chain. A hook is discovered by zarlcode/catalog (markdown
// frontmatter + a shell script body); pre_tool hooks run before the tool
// executes and may block the call, post_tool hooks inspect the outcome and
// may convert it into a failure the model sees.
//
// The hook command runs via `sh -c` in the workspace root with a JSON
// payload on stdin (event, tool name, arguments, and — post_tool — the
// success flag and error string) plus ZARLCODE_HOOK_EVENT /
// ZARLCODE_TOOL_NAME / ZARLCODE_WORKSPACE_ROOT in the environment. Exit zero
// passes. A non-zero exit from a blocking hook rejects the call through the
// guardrail chain, carrying the tail of the hook's output so the model (and
// the transcript) see why; a non-blocking hook's failure is logged and
// ignored.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// GuardrailName is the identifier the hook guardrail reports in chain
// listings and rejection messages.
const GuardrailName = "hooks"

// outputTailBytes caps how much hook output a rejection carries into the
// failed ToolResult — enough to explain the block without flooding the
// conversation a small model has to keep in its window.
const outputTailBytes = 2048

type compiled struct {
	hook    catalog.Hook
	matcher *regexp.Regexp // nil matches every tool
}

func (c compiled) matches(name tools.ToolName) bool {
	return c.matcher == nil || c.matcher.MatchString(string(name))
}

// Guardrail runs the discovered command hooks around tool dispatch. It
// satisfies guardrails.PreCall (pre_tool hooks) and guardrails.PostCall
// (post_tool hooks). Stateless between calls, so the concurrent dispatch
// contract of GuardedSource holds without extra locking.
type Guardrail struct {
	root      string
	pre, post []compiled
}

// NewGuardrail compiles defs into an armed guardrail rooted at the workspace
// root. Matchers are anchored to the whole tool name (`write|edit` matches
// exactly those tools). Catalog loading already validated each matcher, so a
// compile failure here means the definition bypassed LoadHooks.
func NewGuardrail(root string, defs []catalog.Hook) (*Guardrail, error) {
	g := &Guardrail{root: root}
	for _, def := range defs {
		c := compiled{hook: def}
		if def.Matcher != "" {
			re, err := regexp.Compile("^(?:" + def.Matcher + ")$")
			if err != nil {
				return nil, fmt.Errorf("hook %q: compile matcher %q: %w", def.Name, def.Matcher, err)
			}
			c.matcher = re
		}
		switch def.Event {
		case catalog.HookPreTool:
			g.pre = append(g.pre, c)
		case catalog.HookPostTool:
			g.post = append(g.post, c)
		default:
			return nil, fmt.Errorf("hook %q: unknown event %q", def.Name, def.Event)
		}
	}
	return g, nil
}

// Name identifies the guardrail in chain listings and rejection prefixes.
func (g *Guardrail) Name() string { return GuardrailName }

// Empty reports whether no hooks were compiled — the caller skips arming the
// guardrail entirely rather than paying a no-op chain slot.
func (g *Guardrail) Empty() bool { return g == nil || (len(g.pre) == 0 && len(g.post) == 0) }

// payload is the JSON document a hook command receives on stdin.
type payload struct {
	Event         catalog.HookEvent    `json:"event"`
	WorkspaceRoot string               `json:"workspace_root"`
	ToolName      string               `json:"tool_name"`
	ToolID        string               `json:"tool_id,omitempty"`
	Arguments     tools.ToolParameters `json:"arguments,omitempty"`
	// Success / Error are populated for post_tool only.
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Before runs every matching pre_tool hook in discovery order. The first
// blocking hook that exits non-zero rejects the call; non-blocking failures
// are logged and skipped.
func (g *Guardrail) Before(ctx context.Context, call tools.ToolCall) error {
	return g.fireAll(ctx, g.pre, payload{
		Event:         catalog.HookPreTool,
		WorkspaceRoot: g.root,
		ToolName:      string(call.ToolName),
		ToolID:        call.ID.String(),
		Arguments:     call.Arguments,
	})
}

// Inspect runs every matching post_tool hook in discovery order, handing each
// the call plus the outcome (success flag and error string). The first
// blocking hook that exits non-zero replaces the result with a failure.
func (g *Guardrail) Inspect(ctx context.Context, call tools.ToolCall, result *tools.ToolResult, execErr error) error {
	success := execErr == nil && result != nil && result.Success
	p := payload{
		Event:         catalog.HookPostTool,
		WorkspaceRoot: g.root,
		ToolName:      string(call.ToolName),
		ToolID:        call.ID.String(),
		Arguments:     call.Arguments,
		Success:       &success,
	}
	switch {
	case execErr != nil:
		p.Error = execErr.Error()
	case result != nil:
		p.Error = result.Error
	}
	return g.fireAll(ctx, g.post, p)
}

func (g *Guardrail) fireAll(ctx context.Context, hooks []compiled, p payload) error {
	name := tools.ToolName(p.ToolName)
	for _, c := range hooks {
		if !c.matches(name) {
			continue
		}
		if err := g.fire(ctx, c.hook, p); err != nil {
			return err
		}
	}
	return nil
}

// fire runs one hook command to completion under its timeout. A non-nil
// return means a blocking hook rejected the call; every other outcome —
// success, or a non-blocking failure — returns nil.
func (g *Guardrail) fire(ctx context.Context, h catalog.Hook, p payload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("hook %q: encode payload: %w", h.Name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", h.Command)
	cmd.Dir = g.root
	cmd.Stdin = bytes.NewReader(body)
	// On timeout the kill lands on sh, but a forked child can hold the
	// output pipe open past the deadline; WaitDelay force-closes the pipes
	// so a runaway hook can't stall tool dispatch beyond its budget.
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(),
		"ZARLCODE_HOOK_EVENT="+string(p.Event),
		"ZARLCODE_TOOL_NAME="+p.ToolName,
		"ZARLCODE_WORKSPACE_ROOT="+g.root,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		runErr = fmt.Errorf("%w (%w)", ctxErr, runErr)
	}
	if !h.Blocking {
		slog.WarnContext(ctx, "non-blocking hook command failed",
			"hook", h.Name, "tool", p.ToolName, "error", runErr)
		return nil
	}
	if tail := outputTail(out); tail != "" {
		return fmt.Errorf("hook %q: %w: %s", h.Name, runErr, tail)
	}
	return fmt.Errorf("hook %q: %w", h.Name, runErr)
}

// outputTail returns the trailing slice of a hook's combined output, capped
// at outputTailBytes — the end of the output is where a failing script's
// reason usually lands.
func outputTail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) <= outputTailBytes {
		return s
	}
	return "…" + s[len(s)-outputTailBytes:]
}
