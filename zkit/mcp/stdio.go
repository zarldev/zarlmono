package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/zexec"
)

// stdioTransport launches an MCP server as a subprocess and speaks
// JSON-RPC 2.0 over newline-delimited JSON on its stdin/stdout. A single
// reader goroutine pumps server responses into per-request reply channels
// keyed by request ID.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	writeM sync.Mutex

	pendingM sync.Mutex
	// pending is keyed by the raw bytes of the request ID (rendered
	// as a string for map use). Storing the wire form lets us match
	// numeric IDs, string IDs, and null IDs uniformly — converting
	// to a typed Go int would re-introduce the "missing vs zero"
	// conflation the wire layer just fixed.
	pending map[string]chan rpcResponse

	notifM   sync.Mutex
	notifier func(method string, params json.RawMessage)

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
}

// OnNotification registers (or replaces) the handler invoked for every
// incoming server-pushed JSON-RPC notification. nil clears the handler.
// Called at most once per Client — *Client multiplexes by method.
func (t *stdioTransport) OnNotification(handler func(method string, params json.RawMessage)) {
	t.notifM.Lock()
	defer t.notifM.Unlock()
	t.notifier = handler
}

func (t *stdioTransport) dispatchNotification(method string, params json.RawMessage) {
	t.notifM.Lock()
	h := t.notifier
	t.notifM.Unlock()
	if h != nil {
		h(method, params)
	}
}

func newStdioTransport(command string, args []string, env map[string]string) (*stdioTransport, error) {
	if command == "" {
		return nil, errors.New("stdio transport: command is required")
	}

	cmd := exec.CommandContext(context.Background(), command, args...)
	cmd.Env = zexec.MinimalEnv(env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}

	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[string]chan rpcResponse),
		done:    make(chan struct{}),
	}

	go t.readLoop(stdout)
	go t.drainStderr(stderr, command)

	return t, nil
}

func (t *stdioTransport) readLoop(stdout io.Reader) {
	defer close(t.done)
	scanner := bufio.NewScanner(stdout)
	// MCP responses can carry large tool outputs; allow up to 16 MiB per line.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Debug("mcp stdio: non-JSON line", "line", string(line))
			continue
		}
		if len(resp.ID) == 0 {
			// No id member → this is a server-pushed notification.
			// Re-parse with the notification shape so we keep Params as
			// RawMessage for the subscriber (rpcResponse drops Method).
			//
			// Earlier this check was `resp.ID == 0` against an int field,
			// which conflated the perfectly-valid request ID 0 with
			// "no id present". Switching to RawMessage emptiness makes
			// the distinction structural.
			var note rpcNotificationIn
			if err := json.Unmarshal(line, &note); err == nil && note.Method != "" {
				t.dispatchNotification(note.Method, note.Params)
			}
			continue
		}
		key := string(resp.ID)
		t.pendingM.Lock()
		ch, ok := t.pending[key]
		delete(t.pending, key)
		t.pendingM.Unlock()
		if ok {
			ch <- resp
		}
	}
	// On EOF / error fail any still-pending calls so callers unblock.
	t.pendingM.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.pendingM.Unlock()
}

// drainStderr pumps each newline-delimited line from the MCP
// subprocess's stderr into the slog default handler, with two
// defences before the line lands in the log file:
//
//  1. Secret redaction. MCP servers can receive caller-supplied
//     env via zexec.MinimalEnv(env); a buggy server printing
//     "DEBUG: API_KEY=sk-…" or "Authorization: Bearer …" used to
//     spill the credential into ~/.zarlcode/cache/logs/ verbatim.
//     redactSecrets masks the value of any common token-shaped
//     key=value or header-shaped "Bearer …" pattern before
//     logging.
//
//  2. Length cap. A misbehaving server can print megabytes of
//     stderr per second; capping the per-line length stops a
//     single bad release from filling the log directory.
//
// Level is Debug rather than Info — interactive sessions don't
// surface MCP stderr by default; operators that need to debug a
// server can run with LOG_LEVEL=debug. This matches the rest of
// the package's "log signal, not noise" convention.
func (t *stdioTransport) drainStderr(r io.Reader, command string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxStderrLineBytes)
	for scanner.Scan() {
		slog.Debug("mcp stdio stderr",
			"command", command,
			"line", redactSecrets(truncateLine(scanner.Text(), maxStderrLineBytes)))
	}
}

// maxStderrLineBytes caps both the scanner buffer and the
// truncation budget. Generous enough that real stack traces and
// JSON dumps survive intact; tight enough that a runaway server
// can't fill the log device.
const maxStderrLineBytes = 4 * 1024

// truncateLine caps s at n bytes, appending an ellipsis marker
// when the cap fires so log readers can tell the line was clipped.
// Operates on bytes, not runes — stderr is opaque and we're
// optimising for the log file size cap, not display.
func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…[truncated]"
}

// secretPatterns matches the value portion of a "credential-shaped"
// assignment so it can be replaced with REDACTED. Each pattern
// captures the leading "key=" / "key: " / "Bearer " prefix in
// group 1 and the value in group 2; the replacement keeps group 1
// and masks group 2.
//
// Order matters: the more specific "Authorization: Bearer X"
// pattern runs FIRST so it consumes the whole "Bearer sk-…"
// payload. If the generic key=value pattern ran first, it would
// match "Authorization: " + value=Bearer and leave the real token
// exposed on the tail of the line.
//
// Coverage:
//   - "Authorization: Bearer …" / "Authorization: Token …"
//   - "Key: …" / "Token: …" / "Password: …" log-line shapes.
//   - foo_token=, foo_secret=, foo_password=, foo_passwd=,
//     foo_key=, foo_apikey=, foo_api_key= (case-insensitive key,
//     optional whitespace around =).
//
// Values are matched as runs of non-whitespace (so single-line
// stderr only — multi-line PEM blobs aren't this function's
// problem; the level-gate handles those).
var secretPatterns = []*regexp.Regexp{
	// "Authorization: Bearer X" / "Authorization: Token X" /
	// "Token: X" / "Password: X". Note that the optional
	// "bearer "/"token " label is INSIDE group 2 alongside the
	// value, so the whole "Bearer sk-…" gets masked in one shot.
	// Leaving it in group 1 produced "Authorization: Bearer REDACTED"
	// which the next pattern then re-matched (auth* prefix +
	// "Bearer" as value) and ended up with "REDACTED REDACTED".
	regexp.MustCompile(`(?i)(\b(?:authorization|bearer|token|password)\s*:\s*)((?:bearer\s+|token\s+)?\S+)`),
	regexp.MustCompile(
		`(?i)((?:[a-z0-9_-]*(?:token|secret|password|passwd|api[_-]?key|apikey|access[_-]?key|auth)[a-z0-9_-]*)\s*[:=]\s*)(\S+)`,
	),
}

// redactSecrets walks each pattern in [secretPatterns] and
// rewrites the value half of every match to "REDACTED". Idempotent
// on already-redacted input (REDACTED is itself a value the
// patterns leave intact by replacing it with itself).
func redactSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}REDACTED")
	}
	return s
}

// registerPending reserves a reply channel keyed by the raw bytes
// of the request ID. The caller has already serialised the request,
// so the wire-form is exactly what readLoop will see when the
// response comes back.
func (t *stdioTransport) registerPending(id json.RawMessage) chan rpcResponse {
	ch := make(chan rpcResponse, 1)
	t.pendingM.Lock()
	t.pending[string(id)] = ch
	t.pendingM.Unlock()
	return ch
}

func (t *stdioTransport) unregisterPending(id json.RawMessage) {
	t.pendingM.Lock()
	delete(t.pending, string(id))
	t.pendingM.Unlock()
}

func (t *stdioTransport) writeFrame(ctx context.Context, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	b = append(b, '\n')

	t.writeM.Lock()
	defer t.writeM.Unlock()

	// os.exec pipes have no write deadline, so do the (possibly blocking) write
	// on a goroutine and select on the caller's ctx / transport shutdown. A
	// server that stops draining its stdin would otherwise wedge this call
	// forever and, because writeM is held, every other Call/Notify with it.
	werr := make(chan error, 1)
	go func() {
		_, e := t.stdin.Write(b)
		werr <- e
	}()
	select {
	case e := <-werr:
		return e
	case <-t.done:
		return errors.New("stdio transport closed")
	case <-ctx.Done():
		// The write is stuck (server not draining stdin): the frame is only
		// partially on the wire and the stream is desynced. Tear the transport
		// down — synchronously, still under writeM, so no other writer can
		// interleave a corrupt frame; closing stdin unblocks the orphaned
		// write goroutine.
		_ = t.Close()
		return ctx.Err()
	}
}

func (t *stdioTransport) Call(ctx context.Context, req rpcRequest) (rpcResponse, error) {
	ch := t.registerPending(req.ID)

	if err := t.writeFrame(ctx, req); err != nil {
		t.unregisterPending(req.ID)
		return rpcResponse{}, fmt.Errorf("write request: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return rpcResponse{}, errors.New("stdio transport closed before response")
		}
		return resp, nil
	case <-ctx.Done():
		t.unregisterPending(req.ID)
		return rpcResponse{}, ctx.Err()
	case <-t.done:
		return rpcResponse{}, errors.New("stdio transport closed before response")
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (t *stdioTransport) Notify(ctx context.Context, n rpcNotification) error {
	return t.writeFrame(ctx, n)
}

func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		// Closing stdin signals the server to exit cleanly.
		if err := t.stdin.Close(); err != nil {
			t.closeErr = err
		}
		// Give the process a chance to exit on its own. If it doesn't
		// exit within a short window we kill it.
		exited := make(chan error, 1)
		go func() { exited <- t.cmd.Wait() }()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			_ = t.cmd.Process.Kill()
			<-exited
		}
	})
	return t.closeErr
}
