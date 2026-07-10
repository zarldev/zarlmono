package code

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// posToUint64 converts a cursor position to uint64, treating negative
// values as zero (invalid cursor = read from start).
func posToUint64(v int) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// BashOutputTool reads incremental stdout/stderr from a background
// process started via bash(background=true). The agent passes the
// cursor returned by the previous call to avoid re-reading the same
// content — same pattern as Claude Code's BashOutput, so models
// already trained on that surface know what to do.
type BashOutputTool struct{ mgr *ProcessManager }

// BashOutputArgs is the typed argument struct BashOutputTool.Execute
// decodes into via tools.DecodeArgs. Cursor and max_lines are
// integers, not uint64, because JSON numbers + the runner's
// parameter normalisation prefer int.
type BashOutputArgs struct {
	ProcessID    ProcessID          `json:"process_id" doc:"Process id returned by bash(background=true)."`
	StdoutCursor int                `json:"stdout_cursor,omitempty" doc:"Last-seen stdout cursor; omit on first call to read from start."`
	StderrCursor int                `json:"stderr_cursor,omitempty" doc:"Last-seen stderr cursor; omit on first call to read from start."`
	MaxLines     int                `json:"max_lines,omitempty" doc:"Cap returned lines per stream (default 1000, 0 = no cap)."`
	Output       tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, header + stdout/stderr sections) or \"json\"."`
}

// NewBashOutputTool returns the tool that reads buffered output from a
// background process managed by m.
func NewBashOutputTool(m *ProcessManager) *BashOutputTool { return &BashOutputTool{mgr: m} }

// Definition advertises bash_output with process_id (required),
// stdout/stderr cursors, max_lines, and a labeled|json output enum;
// polling never mutates.
func (*BashOutputTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameBashOutput,
		Description: "Poll a background process for new stdout/stderr lines since the last cursor. " +
			"Returns labelled plaintext — a header row of cursors + counters, then `--- stdout ---` / " +
			"`--- stderr ---` sections with the new lines indented; set output=\"json\" for " +
			"{running, exit_code?, stdout, stderr, stdout_cursor, stderr_cursor, dropped_*} instead. " +
			"Pass the returned cursors to the next call to read only new lines. " +
			"Non-zero dropped_* means lines rotated out of the ring buffer between reads — " +
			"the process is chatty enough to throttle output.",
		Parameters: tools.SchemaFor[BashOutputArgs](),
	}
}

// BashOutputResult is bash_output's structured Data: the polled snapshot
// plus the requested output mode. A consumer renders from Snapshot
// directly; the model sees String(): labelled sections or the JSON
// snapshot, per Output.
type BashOutputResult struct {
	Snapshot OutputSnapshot
	Output   tools.OutputFormat
}

// String renders the model-facing form for the requested output mode. Stdout
// and stderr are run through tools.RedactSecrets first — the same best-effort
// scrub the foreground bash path applies — so a backgrounded `printenv` (or any
// command that prints a token) doesn't bypass redaction on its way into the
// conversation history.
func (r BashOutputResult) String() string {
	snap := r.Snapshot
	snap.Stdout = redactLines(snap.Stdout)
	snap.Stderr = redactLines(snap.Stderr)
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(snap)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderBashOutputLabeled(snap)
}

// redactLines returns a redacted copy of lines (the input slice is not
// mutated, so the raw OutputSnapshot the event carries is untouched).
func redactLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = tools.RedactSecrets(ln)
	}
	return out
}

// pollBashOutput validates process_id, applies default maxLines, and
// reads the latest output snapshot from the process manager. Error
// paths are already typed.
func pollBashOutput(mgr *ProcessManager, args BashOutputArgs) (OutputSnapshot, error) {
	if args.ProcessID == "" {
		return OutputSnapshot{}, tools.Validation("bash_output", "process_id required")
	}
	maxLines := args.MaxLines
	if maxLines == 0 {
		maxLines = 1000
	}
	snap, err := mgr.Output(args.ProcessID, posToUint64(args.StdoutCursor), posToUint64(args.StderrCursor), maxLines)
	if err != nil {
		return OutputSnapshot{}, tools.NotFound("bash_output", err.Error())
	}
	return snap, nil
}

// Execute requires process_id, defaults max_lines to 1000, and reads
// the incremental snapshot from the process manager at the supplied
// cursors (negative cursors read from the start; unknown ids are
// NotFound). The result's String() redacts secrets line by line before
// the model sees the output.
func (t *BashOutputTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[BashOutputArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	snap, err := pollBashOutput(t.mgr, args)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	return tools.Success(call.ID, BashOutputResult{Snapshot: snap, Output: args.Output.Resolve()}), nil
}

// StopProcessTool kills a background process. SIGTERM by default
// with a 5s escalate-to-SIGKILL grace; explicit signal override
// available for the rare case the agent needs immediate KILL or a
// gentle INT.
type StopProcessTool struct{ mgr *ProcessManager }

// StopProcessArgs is the typed argument struct StopProcessTool.Execute
// decodes into via tools.DecodeArgs. Signal stays a string and is
// matched against TERM/KILL/INT inside Execute (the JSON Schema
// declares the enum; the SchemaGuardrail validates membership
// before dispatch).
type StopProcessArgs struct {
	ProcessID ProcessID `json:"process_id" doc:"Process id returned by bash(background=true)."`
	Signal    string    `json:"signal,omitempty" enum:"TERM,KILL,INT" doc:"Optional signal. Default TERM. Use KILL for immediate termination, INT to send Ctrl+C semantics first."`
}

// NewStopProcessTool returns the tool that terminates a background
// process managed by m.
func NewStopProcessTool(m *ProcessManager) *StopProcessTool { return &StopProcessTool{mgr: m} }

// Definition advertises stop_process with process_id (required) and an
// optional TERM/KILL/INT signal enum; the spec text documents the
// SIGTERM-with-5s-grace default and idempotent exit-code semantics.
func (*StopProcessTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameStopProcess,
		Description: "Terminate a background process. SIGTERM with 5s grace before SIGKILL by default. " +
			"Returns {process_id, exit_code, killed_at}. Idempotent — calling on an already-exited " +
			"process succeeds and returns the recorded exit code.",
		Parameters: tools.SchemaFor[StopProcessArgs](),
	}
}

// Execute requires process_id, maps the signal string
// (case-insensitively) to SIGKILL or SIGINT — anything else falls back
// to SIGTERM — and delegates to the manager's Kill, returning
// {process_id, exit_code, killed_at}; unknown ids are NotFound.
func (t *StopProcessTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[StopProcessArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.ProcessID == "" {
		return tools.Failure(call.ID, tools.Validation("stop_process", "process_id required")), nil
	}
	sig := syscall.SIGTERM
	switch strings.ToUpper(args.Signal) {
	case "KILL":
		sig = syscall.SIGKILL
	case "INT":
		sig = syscall.SIGINT
	}
	exitCode, err := t.mgr.Kill(args.ProcessID, sig)
	if err != nil {
		return tools.Failure(call.ID, tools.NotFound("stop_process", err.Error())), nil
	}
	return tools.Success(call.ID, map[string]any{
		"process_id": args.ProcessID,
		"exit_code":  exitCode,
		"killed_at":  time.Now().Format(time.RFC3339),
	}), nil
}

// ListProcessesTool returns a snapshot of every tracked process —
// live processes plus those that exited recently enough to still be
// in the reap window. The agent uses this to discover process_ids
// it forgot, audit what's running before spawning more work, and
// reconcile state after a long pause.
type ListProcessesTool struct{ mgr *ProcessManager }

// ListProcessesArgs carries only the output-format choice — the tool
// otherwise takes no arguments.
type ListProcessesArgs struct {
	Output tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, one block per process) or \"json\"."`
}

// NewListProcessesTool returns the tool that enumerates the background
// processes managed by m.
func NewListProcessesTool(m *ProcessManager) *ListProcessesTool { return &ListProcessesTool{mgr: m} }

// Definition advertises list_processes whose only parameter is the
// labeled|json output enum; listing never mutates.
func (*ListProcessesTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameListProcesses,
		Description: "List background processes started via bash(background=true). Returns labelled " +
			"plaintext — one process per block with id, pid, state, age, command, cwd, and line counts; " +
			"set output=\"json\" for [{process_id, command, pid, cwd, started_at, running, exited_at?, " +
			"exit_code?, stdout_lines, stderr_lines}] instead. Includes still-running and recently-exited " +
			"(within 60s) processes, newest first.",
		Parameters: tools.SchemaFor[ListProcessesArgs](),
	}
}

// ListProcessesResult is list_processes's structured Data: the process
// snapshot plus the requested output mode. A consumer renders from Procs
// directly; the model sees String(): labelled blocks or the JSON list,
// per Output.
type ListProcessesResult struct {
	Procs  []ProcessInfo
	Output tools.OutputFormat
}

// String renders the model-facing form for the requested output mode.
func (r ListProcessesResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(map[string]any{
			"processes": r.Procs,
			"count":     len(r.Procs),
		})
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderListProcessesLabeled(r.Procs)
}

// Execute snapshots every tracked process from the manager — running
// plus recently exited — and returns a ListProcessesResult rendered
// per the requested output format; nothing past argument decoding can
// fail.
func (t *ListProcessesTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[ListProcessesArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	return tools.Success(call.ID, ListProcessesResult{Procs: t.mgr.List(), Output: args.Output.Resolve()}), nil
}

func renderBashOutputLabeled(snap OutputSnapshot) string {
	var b strings.Builder
	state := "running"
	if !snap.Running {
		if snap.ExitCode != nil {
			state = fmt.Sprintf("exited (%d)", *snap.ExitCode)
		} else {
			state = "exited"
		}
	}
	fmt.Fprintf(&b, "process: %s  %s  stdout_cursor: %d  stderr_cursor: %d\n",
		snap.ID, state, snap.StdoutCursor, snap.StderrCursor)
	if snap.StdoutDroppedSince > 0 || snap.StderrDroppedSince > 0 {
		fmt.Fprintf(&b, "dropped_stdout: %d  dropped_stderr: %d\n",
			snap.StdoutDroppedSince, snap.StderrDroppedSince)
	}
	writeStream(&b, "stdout", snap.Stdout)
	writeStream(&b, "stderr", snap.Stderr)
	return strings.TrimRight(b.String(), "\n")
}

// writeStream emits one `--- name (N new lines) ---` section + the
// indented lines. Hidden when the stream has zero new lines so a
// quiet poll doesn't waste tokens advertising emptiness.
func writeStream(b *strings.Builder, name string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n--- %s (%d new line", name, len(lines))
	if len(lines) != 1 {
		b.WriteString("s")
	}
	b.WriteString(") ---\n")
	for _, ln := range lines {
		b.WriteString("  ")
		b.WriteString(ln)
		b.WriteString("\n")
	}
}

func renderListProcessesLabeled(procs []ProcessInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "processes: %d\n", len(procs))
	if len(procs) == 0 {
		b.WriteString("(none running)")
		return b.String()
	}
	now := time.Now()
	for _, p := range procs {
		state := "running"
		age := now.Sub(p.StartedAt)
		if !p.Running {
			state = fmt.Sprintf("exited(%d)", p.ExitCode)
			if !p.ExitedAt.IsZero() {
				age = p.ExitedAt.Sub(p.StartedAt)
			}
		}
		fmt.Fprintf(&b, "  %s  pid %d  %s  %s  %s\n",
			p.ID, p.PID, state, formatAge(age), oneLineProc(p.Command))
		fmt.Fprintf(&b, "    cwd: %s\n", p.CWD)
		fmt.Fprintf(&b, "    stdout: %d lines  stderr: %d lines\n",
			p.StdoutLines, p.StderrLines)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatAge renders a time.Duration as a compact human-readable
// suffix. Sub-minute durations show seconds; minutes show m+s.
// Mirrors the cadence that matters for the model's "is this still
// running?" intuition.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// oneLineProc collapses internal whitespace in a command string so a
// multi-line `bash -c "..."` invocation still fits on the process
// header row.
func oneLineProc(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
