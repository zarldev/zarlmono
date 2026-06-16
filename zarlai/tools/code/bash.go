package code

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	bashDefaultTimeout = 300 // seconds
	bashMaxTimeout     = 600
	bashMaxOutput      = 1 * 1024 * 1024 // 1 MB
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// BashTool runs a shell command with cwd set to the workspace root.
type BashTool struct{ ws Workspace }

func NewBashTool(ws Workspace) *BashTool { return &BashTool{ws: ws} }

func (t *BashTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "bash",
		Description: "Execute a shell command in the workspace. Output is streamed to a 1 MB buffer; longer output is truncated. Default timeout 300s, max 600s.",
		Parameters: service.Parameters{
			{Name: "command", Type: service.ParamString, Description: "Shell command (interpreted by /bin/sh -c).", Required: true},
			{Name: "timeout_seconds", Type: service.ParamInteger, Description: "Override timeout (max 600).", Required: false},
			{Name: "description", Type: service.ParamString, Description: "Short human-readable label for the call.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *BashTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	cmdStr := call.Arguments.String("command", "")
	if cmdStr == "" {
		return tools.Failure(call.ID, tools.Validation("bash", "bash: command required")), nil
	}
	timeout := call.Arguments.Int("timeout_seconds", bashDefaultTimeout)
	if timeout <= 0 {
		timeout = bashDefaultTimeout
	}
	if timeout > bashMaxTimeout {
		timeout = bashMaxTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", cmdStr)
	cmd.Dir = t.ws.Root()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

	// exec.Cmd serialises Stdout/Stderr writes when they point to the
	// same writer (its docs guarantee at-most-one-goroutine), and
	// cmd.Wait() blocks until both copy goroutines drain. That gives us
	// the combined-output capture without our own StdoutPipe + reader
	// goroutine race that lost trailing bytes under -race.
	collector := &limitedBuffer{max: bashMaxOutput}
	cmd.Stdout = collector
	cmd.Stderr = collector

	if err := cmd.Start(); err != nil {
		return tools.Failure(call.ID, tools.Transient("bash", fmt.Errorf("bash start: %w", err))), nil
	}
	waitErr := cmd.Wait()

	var b bytes.Buffer
	b.Write([]byte(ansiRe.ReplaceAllString(collector.String(), "")))

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if timedOut {
		fmt.Fprintf(&b, "\n[timed_out after %ds]\n", timeout)
	}
	if collector.truncated() {
		fmt.Fprintf(&b, "\n[output_truncated at %d bytes]\n", bashMaxOutput)
	}

	exitCode := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else if !timedOut {
			fmt.Fprintf(&b, "\n[wait error: %v]\n", waitErr)
		}
	}
	fmt.Fprintf(&b, "\n[exit %d]\n", exitCode)

	return tools.Success(call.ID, b.String()), nil
}

// limitedBuffer is an io.Writer that captures up to max bytes and
// silently discards the rest, flagging the overflow. The mutex is
// belt-and-braces — exec.Cmd already serialises stdout and stderr
// writes when they target the same writer, but the lock keeps
// correctness obvious if a future caller reuses one across goroutines.
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
