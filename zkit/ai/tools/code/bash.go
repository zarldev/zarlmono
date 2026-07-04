package code

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	bashDefaultTimeout = 300 // seconds
	bashMaxTimeout     = 600
	bashMaxOutput      = 1 * 1024 * 1024 // 1 MB
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// longRunningPatterns matches the head of a shell command line that
// hosts a process the user almost never wants to run synchronously
// (servers, file watchers, log followers, dev-mode JS toolchains).
// The bash tool flips background=true silently when one of these
// matches AND the model didn't already pass it, then surfaces a
// "auto-backgrounded" notice in the result so the model knows.
//
// Each pattern is conservative — match the literal binary or
// subcommand at a word boundary, never substring. Patterns that
// would catch real foreground use (`echo daemon`, `git log --tail`)
// are intentionally excluded.
//
// Add a new pattern only when its foreground form has bitten in
// practice and the long-running form is the overwhelming majority of
// real invocations.
var longRunningPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|\s|/)llama-server\b`),
	regexp.MustCompile(`\bzarlcode\s+serve\b`),
	regexp.MustCompile(`\btail\s+(-[a-zA-Z]*f|-f[a-zA-Z]*)\b`),
	regexp.MustCompile(`^\s*watch\s+`),
	regexp.MustCompile(`\bpython3?\s+-m\s+http\.server\b`),
	regexp.MustCompile(`\bjupyter\s+(notebook|lab)\b`),
	regexp.MustCompile(`\b(npm|pnpm|yarn)\s+run\s+(dev|start|serve|watch)\b`),
	regexp.MustCompile(`\b(npm|pnpm|yarn)\s+(dev|start|serve|watch)\b`),
	regexp.MustCompile(`\bnpx\s+(vite|next|wrangler|astro|remix|nuxt|svelte-kit)\s+(dev|start|preview)\b`),
	regexp.MustCompile(`\bnext\s+(dev|start)\b`),
	regexp.MustCompile(`\bvite\b(?:\s+--\w+)*\s*$`),
	regexp.MustCompile(`\bnode\s+.*--watch\b`),
	regexp.MustCompile(`\bjournalctl\s+(-[a-zA-Z]*f|-f[a-zA-Z]*)\b`),
	regexp.MustCompile(`\bdocker\s+(logs|compose\s+logs)\s+(-[a-zA-Z]*f|-f[a-zA-Z]*)\b`),
	regexp.MustCompile(`\bdocker\s+compose\s+up\b(?:\s+--\w+)*\s*$`),
	regexp.MustCompile(`\btensorboard\s+--`),
	regexp.MustCompile(`\bmlflow\s+(server|ui)\b`),
}

// looksLongRunning reports whether cmdStr matches one of the
// long-running command patterns, returning the matched pattern's
// source so the result can tell the model which signature triggered
// the auto-flip.
func looksLongRunning(cmdStr string) (bool, string) {
	for _, p := range longRunningPatterns {
		if p.MatchString(cmdStr) {
			return true, p.String()
		}
	}
	return false, ""
}

// shellPath picks the interpreter for the bash tool. Prefers /bin/bash
// when present so bash-isms ([[ ]], arrays, <(...), pipefail) actually
// work — the tool is named "bash" and the model writes accordingly.
// Falls back to /bin/sh on systems without bash. Resolved once at
// startup; the result is cached for the process lifetime.
var shellPath = sync.OnceValue(func() string {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
})

// BashTool runs a shell command with cwd set to the workspace root.
//
// procMgr (optional) routes background=true through a managed
// process with in-memory output capture so the agent can poll via
// bash_output and kill via stop_process. When nil, background mode
// falls back to the legacy "detach + write to log file" path. The
// zarlcode wires a real ProcessManager via WithProcessManager;
// other consumers (zarlai, headless tests) can omit it and keep
// the simpler log-file behaviour.
type BashTool struct {
	ws      Workspace
	procMgr *ProcessManager
	sandbox Sandboxer
	env     map[string]string
}

// BashArgs is the typed argument struct BashTool.Execute decodes
// into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type BashArgs struct {
	Command        string `json:"command" doc:"Shell command (interpreted by /bin/bash -c when available, else /bin/sh -c)."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" doc:"Override timeout (max 600). Ignored when background=true."`
	Background     bool   `json:"background,omitempty" doc:"Start the process detached; return immediately with pid + log path. Use for servers, file watchers, or anything that should outlive this tool call."`
	Description    string `json:"description,omitempty" doc:"Short human-readable label for the call."`
}

// BashOption tunes BashTool construction. The variadic options
// pattern is consistent with the rest of pkg/ai/tools.
type BashOption func(*BashTool)

// WithProcessManager enables managed background processes — the
// bash tool returns a process_id usable by bash_output / kill_bash /
// list_processes instead of a raw PID + log path.
func WithProcessManager(m *ProcessManager) BashOption {
	return func(t *BashTool) { t.procMgr = m }
}

// WithSandbox confines every foreground command behind sb (background
// commands go through the ProcessManager, which carries its own
// sandboxer — wire the same instance to both or they drift). Nil is a
// no-op so callers can pass through an unset optional.
func WithSandbox(sb Sandboxer) BashOption {
	return func(t *BashTool) { t.sandbox = sb }
}

// WithEnv appends child-process environment variables to every shell command.
// Values override the inherited process environment for the spawned shell and
// its children.
func WithEnv(env map[string]string) BashOption {
	return func(t *BashTool) { t.env = cloneEnvMap(env) }
}

// NewBashTool returns the shell tool rooted at ws. Without a process
// manager (WithProcessManager), background execution degrades to the
// legacy detach-and-log path.
func NewBashTool(ws Workspace, opts ...BashOption) *BashTool {
	t := &BashTool{ws: ws}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Definition advertises bash with command (required), timeout_seconds,
// background, and description parameters; the spec text documents the
// 1MB output cap, 300s default / 600s max timeout, background process
// management, and the pkill -f footgun. Mutates stays false — a shell
// command is not a tracked file edit and must not count as patch-producing
// work — but AffectsWorkspace is true: a command can write files or mutate
// git/env state, so cache-invalidation and plan-first gating treat it as
// workspace-changing via ChangesWorkspace.
func (t *BashTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:             ToolNameBash,
		AffectsWorkspace: true,
		Description: "Execute a shell command in the workspace. " +
			"Synchronous (default): blocks until exit, returns stdout+stderr+code; output capped at 1MB by the tool, then trimmed to the tail by the runner's 50KB / 2000-line tool-result cap (full output is spilled to disk and the path is included in the footer); timeout 300s default, 600s max. " +
			"Background (`background: true`): returns immediately with a process_id; manage with bash_output / stop_process / list_processes. Use for servers, watchers, dev-mode toolchains. " +
			"**Never `pkill -f <pattern>`** — pkill matches against /proc/N/cmdline including the bash shell you're running it in, so it kills its own shell and you get `[exit -1]`. Use `pgrep -x <name>` then `kill <pid>`.",
		Parameters: tools.SchemaFor[BashArgs](),
	}
}

// Execute runs the command via /bin/bash -c (falling back to /bin/sh)
// with cwd at the workspace root. background=true — or a command
// matching longRunningPatterns, which is auto-backgrounded with a
// notice — returns immediately with a process handle. Foreground runs
// cap output at bashMaxOutput (1 MB), enforce the clamped timeout by
// SIGKILLing the whole process group, strip ANSI, redact secrets, and
// append the exit code; the ProcessEffect carries timeout/truncation
// flags.
func (t *BashTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[BashArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Command == "" {
		return tools.Failure(call.ID, tools.Validation("bash", "command required")), nil
	}
	if args.Background {
		return t.executeBackground(ctx, call, args.Command, "")
	}
	// Auto-background safety net: certain commands almost always denote
	// a long-running process the model should have flagged background.
	// Running them synchronously stalls the turn until the 300s timeout
	// fires and leaves no way to interact with the spawned process.
	// Flip silently and tell the model in the result body so it knows
	// the call returned a pid instead of stdout.
	if hit, pattern := looksLongRunning(args.Command); hit {
		return t.executeBackground(ctx, call, args.Command, pattern)
	}
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = bashDefaultTimeout
	}
	if timeout > bashMaxTimeout {
		timeout = bashMaxTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd, cleanupStdin, err := t.newCmd(runCtx, args.Command)
	if err != nil {
		return nil, err
	}
	defer cleanupStdin()

	// Default cancel only signals the leader, so child processes
	// (e.g. `sleep` spawned by sh) keep the pipe write end open and
	// Wait blocks until they exit on their own. SIGKILL the whole
	// process group instead so the pipe drains immediately.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	collector := &limitedBuffer{max: bashMaxOutput}
	cmd.Stdout = collector
	cmd.Stderr = collector

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("bash start: %w", err)
	}
	waitErr := cmd.Wait()

	var b bytes.Buffer
	b.WriteString(tools.RedactSecrets(ansiRe.ReplaceAllString(collector.String(), "")))

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if timedOut {
		fmt.Fprintf(&b, "\n[timed_out after %ds]\n", timeout)
	}
	if collector.truncated() {
		fmt.Fprintf(&b, "\n[output_truncated at %d bytes]\n", bashMaxOutput)
	}

	exitCode := 0
	if waitErr != nil {
		if ee, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			exitCode = ee.ExitCode()
		} else if !timedOut {
			fmt.Fprintf(&b, "\n[wait error: %v]\n", waitErr)
		}
	}
	fmt.Fprintf(&b, "\n[exit %d]\n", exitCode)
	if t.sandbox != nil && exitCode != 0 && looksLikeDenial(b.String()) {
		b.WriteString(sandboxDenialHint + "\n")
	}

	effect := tools.NewProcessEffect(args.Command, exitCode)
	effect.Process.TimedOut = timedOut
	effect.Process.OutputTruncated = collector.truncated()
	return tools.Success(call.ID, b.String(), effect), nil
}

// executeBackground starts the command detached and returns
// immediately with handles the agent can use to inspect / kill the
// process. When a ProcessManager is wired (the zarlcode path),
// output is captured in-memory and the result includes a process_id
// for bash_output / stop_process. Otherwise we fall back to the
// legacy log-file path so headless consumers without a manager
// still get a usable background mode.
//
// autoFlipReason, when non-empty, marks this run as an auto-background
// (the model passed background=false but the command matched
// longRunningPatterns). The reason string is the matched pattern so
// the agent's response includes the signature that triggered the
// flip — keeps the behaviour debuggable and lets the model decide
// whether to retry with an explicit background=true once it sees the
// classification.
func (t *BashTool) executeBackground(
	ctx context.Context,
	call tools.ToolCall,
	cmdStr, autoFlipReason string,
) (*tools.ToolResult, error) {
	if t.procMgr != nil {
		return t.executeBackgroundManaged(call, cmdStr, autoFlipReason)
	}
	return t.executeBackgroundLog(ctx, call, cmdStr, autoFlipReason)
}

// executeBackgroundManaged is the ProcessManager-backed background
// flow: spawns through procMgr (which captures stdout/stderr to ring
// buffers + tracks lifecycle), returns a process_id the agent can
// thread into bash_output / stop_process / list_processes.
func (t *BashTool) executeBackgroundManaged(
	call tools.ToolCall,
	cmdStr, autoFlipReason string,
) (*tools.ToolResult, error) {
	id, err := t.procMgr.StartProcess(cmdStr)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("bash", fmt.Errorf("background start: %w", err))), nil
	}
	info, _ := t.procMgr.Info(id)
	var header string
	if autoFlipReason != "" {
		header = fmt.Sprintf(
			"[auto-backgrounded — command matched long-running pattern %q; pass background:true next time to make the intent explicit]\n",
			autoFlipReason,
		)
	}
	body := fmt.Sprintf(
		"%sstarted process_id=%s pid=%d\n\nPoll output with: bash_output(process_id=%q)\nKill with:       stop_process(process_id=%q)\nList all with:   list_processes()\n",
		header,
		id,
		info.PID,
		id,
		id,
	)
	effect := tools.NewProcessEffect(cmdStr, 0)
	effect.Process.Background = true
	effect.Process.ProcessID = id
	effect.Process.PID = info.PID
	effect.Process.AutoBackgrounded = autoFlipReason != ""
	return tools.Success(call.ID, body, effect), nil
}

// executeBackgroundLog is the original log-file fallback used when
// no ProcessManager is wired. Kept verbatim for behavioural
// compatibility with zarlai / other consumers that still rely on
// `tail <path>` and `kill <pid>` semantics.
func (t *BashTool) executeBackgroundLog(
	_ context.Context,
	call tools.ToolCall,
	cmdStr, autoFlipReason string,
) (*tools.ToolResult, error) {
	logFile, err := os.CreateTemp("", "agent-bash-bg-*.log")
	if err != nil {
		return nil, fmt.Errorf("bash: create log: %w", err)
	}
	defer logFile.Close()
	logPath := logFile.Name()

	// Detach from parent context — background processes outlive the
	// tool call.
	cmd, cleanupStdin, err := t.newCmd(context.Background(), cmdStr)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		cleanupStdin()
		return nil, fmt.Errorf("bash background start: %w", err)
	}
	// Child has dup'd the stdin FD by now — release the parent copy.
	// Skipping this on every background launch leaked one FD per call.
	cleanupStdin()
	pid := cmd.Process.Pid

	// Reap the child without blocking — without this, a quick-exiting
	// background process becomes a zombie until the zarlcode exits.
	go func() { _ = cmd.Wait() }()

	var header string
	if autoFlipReason != "" {
		header = fmt.Sprintf(
			"[auto-backgrounded — command matched long-running pattern %q; pass background:true next time to make the intent explicit]\n",
			autoFlipReason,
		)
	}
	body := fmt.Sprintf(
		"%sstarted background pid=%d\nlog: %s\n\nMonitor with: tail %s\nKill with:    kill %d  (or: kill -9 -%d to nuke the whole process group)\n",
		header,
		pid,
		logPath,
		logPath,
		pid,
		pid,
	)
	effect := tools.NewProcessEffect(cmdStr, 0)
	effect.Process.Background = true
	effect.Process.PID = pid
	effect.Process.AutoBackgrounded = autoFlipReason != ""
	return tools.Success(call.ID, body, effect), nil
}

// newCmd prepares a shell command for execution, handling common setup like
// working directory, process group, and stdin redirection.
//
// Returns a cleanup func the caller MUST invoke once the command's
// stdin no longer needs to be open from the parent side. Foreground
// callers run it after cmd.Wait; background callers run it after
// cmd.Start (the child has dup'd the FD by then). Earlier shape
// claimed StartProcess would close the *os.File — not true; the
// parent FD stays open until garbage collection, which under heavy
// bash use leaked the FD pool fast enough to exhaust ulimit.
func (t *BashTool) newCmd(ctx context.Context, cmdStr string) (*exec.Cmd, func(), error) {
	cmd := exec.CommandContext(ctx, shellPath(), "-c", cmdStr)
	cmd.Dir = t.ws.Root()
	// Setsid puts the child in a new session with no controlling
	// terminal. Without this, programs that read passwords directly
	// from /dev/tty (sudo, ssh-add, gpg-agent) bypass our stdin and
	// block waiting for keystrokes nobody can deliver. Setsid implies
	// a new process group, so we don't need Setpgid alongside it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	applyCmdEnv(cmd, t.env)
	// Explicit /dev/null stdin so any program that DOES read stdin
	// (rather than /dev/tty) sees EOF immediately and exits cleanly.
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return nil, nil, fmt.Errorf("bash: open /dev/null: %w", err)
	}
	cmd.Stdin = stdin
	cleanup := func() { _ = stdin.Close() }
	if t.sandbox != nil {
		if err := t.sandbox.Sandbox(cmd); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("bash: sandbox: %w", err)
		}
	}
	return cmd, cleanup, nil
}

// sandboxDenialHint is appended to a failed sandboxed command's output
// when it smells like a kernel denial, so the model attributes the
// error to confinement instead of retrying or "fixing" the path.
// Advisory by design: the original error stays first.
const sandboxDenialHint = "(sandbox: this command runs confined — writes outside the workspace/tmp/tool-caches and reads of personal dotfiles are denied by the kernel, not by the target)"

// looksLikeDenial reports whether output plausibly contains a sandbox
// permission failure worth annotating.
func looksLikeDenial(output string) bool {
	return strings.Contains(output, "Permission denied") ||
		strings.Contains(output, "Operation not permitted") ||
		strings.Contains(output, "permission denied")
}

// limitedBuffer is an io.Writer that captures up to max bytes and
// silently discards the rest, flagging the overflow.
type limitedBuffer struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.over = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.over = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *limitedBuffer) truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

func applyCmdEnv(cmd *exec.Cmd, env map[string]string) {
	if len(env) == 0 {
		return
	}
	cmd.Env = os.Environ()
	for k, v := range env {
		if k == "" {
			continue
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}
}

func cloneEnvMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
