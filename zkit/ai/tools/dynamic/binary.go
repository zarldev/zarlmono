// Package dynamic provides the runtime substrate for self-extending
// agents: a BinaryTool that wraps a compiled CLI as a tools.Tool, a
// Catalog that persists registrations across restarts (via a
// pluggable [Store] — sqlite in production, JSON file in tests), and
// a Registrar that ties them to a tools.Registry.
//
// Convention for binary tools (the contract a CLI must satisfy):
//
//	binary --describe         → prints a tools.ToolSpec JSON to stdout
//	binary --call (stdin)     → reads JSON args, writes JSON result
//
// Result envelope on stdout:
//
//	{ "data":  <any> }    on success
//	{ "error": "msg"  }    on failure
//
// Nonzero exit + stderr text are surfaced to the runner as failures.
package dynamic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/zexec"
)

// DefaultCallTimeout caps a single binary invocation. Tools that need
// longer should be split or use streaming.
const DefaultCallTimeout = 60 * time.Second

// BinaryTool wraps a compiled CLI as a tools.Tool.
type BinaryTool struct {
	spec       tools.ToolSpec
	binaryPath string
	timeout    time.Duration
}

// NewBinaryTool builds a BinaryTool from a catalog entry. Use the
// Registrar to instantiate from catalog data; this constructor is
// exported for tests and for callers that have a spec in hand.
func NewBinaryTool(spec tools.ToolSpec, binaryPath string) *BinaryTool {
	return &BinaryTool{
		spec:       spec,
		binaryPath: binaryPath,
		timeout:    DefaultCallTimeout,
	}
}

// WithTimeout overrides the default per-call timeout.
func (t *BinaryTool) WithTimeout(d time.Duration) *BinaryTool {
	if d > 0 {
		t.timeout = d
	}
	return t
}

// Definition returns the tool's LLM-facing spec. The description
// is suffixed with "(dynamic — registered at runtime, removable
// via unregister_tool)" so the LLM has in-context evidence the tool
// isn't a built-in. Without this hint the LLM has no protocol-level
// way to distinguish dynamic tools from shipped ones and tends to
// refuse unregister requests by inferring "built-in" from training.
func (t *BinaryTool) Definition() tools.ToolSpec {
	out := t.spec
	const hint = " (dynamic — registered at runtime, removable via unregister_tool)"
	if !strings.HasSuffix(out.Description, hint) {
		out.Description += hint
	}
	return out
}

// BinaryPath returns the on-disk path to the wrapped executable.
func (t *BinaryTool) BinaryPath() string {
	return t.binaryPath
}

// callEnvelope is what binary --call writes to stdout.
type callEnvelope struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Capacity caps on the captured stdout / stderr buffers. A
// malicious or buggy dynamic tool used to be able to print until
// the OS killed it — the parent process would allocate without
// bound as it copied from the subprocess pipe into bytes.Buffer.
// 1 MB of stdout is generous for a tool result; 64 KB of stderr
// is plenty for an error trace.
const (
	dynamicStdoutCapBytes = 1 * 1024 * 1024
	dynamicStderrCapBytes = 64 * 1024
)

// cappedWriter discards writes past cap so a runaway subprocess
// can't blow the parent's memory. Write always reports len(p) bytes
// "accepted" so the cmd pipe-pump goroutine keeps draining (the
// kernel pipe buffer is the actual ceiling for what the child can
// shove through; we just refuse to store past cap).
type cappedWriter struct {
	buf bytes.Buffer
	cap int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	avail := w.cap - w.buf.Len()
	if avail > 0 {
		if len(p) <= avail {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:avail])
		}
	}
	return len(p), nil
}

func (w *cappedWriter) Bytes() []byte  { return w.buf.Bytes() }
func (w *cappedWriter) Len() int       { return w.buf.Len() }
func (w *cappedWriter) String() string { return w.buf.String() }

// Execute invokes the binary with --call under the per-call timeout,
// sending the call's arguments as JSON on stdin and parsing a
// callEnvelope from stdout. Runs with a minimal env, caps captured
// stdout/stderr (1 MB / 64 KB), redacts secrets from surfaced stderr,
// and kills the whole process group on timeout.
func (t *BinaryTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	argsJSON, err := json.Marshal(call.Arguments)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("dynamic: marshal args: %v", err)), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, t.binaryPath, "--call")
	zexec.StartProcessGroup(cmd)
	cmd.Env = zexec.MinimalEnv(nil)
	cmd.Stdin = bytes.NewReader(argsJSON)
	stdout := &cappedWriter{cap: dynamicStdoutCapBytes}
	stderr := &cappedWriter{cap: dynamicStderrCapBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			_ = zexec.KillProcessGroup(cmd)
		}
		msg := fmt.Sprintf("dynamic: %s exec failed: %v", t.spec.Name, err)
		if stderr.Len() > 0 {
			msg = fmt.Sprintf("%s; stderr: %s", msg, tools.RedactSecrets(stderr.String()))
		}
		return failureResult(call.ID, msg), nil
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return failureResult(call.ID, fmt.Sprintf("dynamic: %s returned empty stdout", t.spec.Name)), nil
	}

	var env callEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return failureResult(
			call.ID,
			fmt.Sprintf("dynamic: %s stdout not valid envelope JSON: %v", t.spec.Name, err),
		), nil
	}
	if env.Error != "" {
		return failureResult(call.ID, env.Error), nil
	}

	var data any
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			data = string(env.Data) // surface as raw if it isn't structured
		}
	}
	return tools.Success(call.ID, data), nil
}

// DescribeBinary execs `<path> --describe` and parses a ToolSpec. Used
// by Registrar.AddFromBinary so callers can register a tool without
// re-stating its spec — the binary owns its own schema.
//
// stdout / stderr are captured through [cappedWriter] with the same
// caps as Execute. A malicious or buggy binary used to be able to
// emit gigabytes during registration and force memory growth before
// the JSON parse — `cmd.Output()` is unbounded.
func DescribeBinary(ctx context.Context, binaryPath string, timeout time.Duration) (tools.ToolSpec, error) {
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binaryPath, "--describe")
	zexec.StartProcessGroup(cmd)
	cmd.Env = zexec.MinimalEnv(nil)
	stdout := &cappedWriter{cap: dynamicStdoutCapBytes}
	stderr := &cappedWriter{cap: dynamicStderrCapBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			_ = zexec.KillProcessGroup(cmd)
		}
		if stderr.Len() > 0 {
			return tools.ToolSpec{}, fmt.Errorf(
				"describe %q: %w; stderr: %s",
				binaryPath,
				err,
				tools.RedactSecrets(stderr.String()),
			)
		}
		return tools.ToolSpec{}, fmt.Errorf("describe %q: %w", binaryPath, err)
	}
	var spec tools.ToolSpec
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &spec); err != nil {
		return tools.ToolSpec{}, fmt.Errorf("describe %q: parse: %w", binaryPath, err)
	}
	if spec.Name == "" {
		return tools.ToolSpec{}, fmt.Errorf("describe %q: spec missing name", binaryPath)
	}
	return spec, nil
}

func failureResult(callID, msg string) *tools.ToolResult {
	return &tools.ToolResult{
		ToolCallID: callID,
		Success:    false,
		Error:      msg,
		ExecutedAt: time.Now(),
	}
}
