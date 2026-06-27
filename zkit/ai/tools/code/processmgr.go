package code

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
)

// ProcessManager tracks the lifecycle of background shell processes
// spawned by the bash tool. Replaces the earlier "redirect to a log
// file, return the raw pid" approach with in-memory output capture
// + structured kill semantics — so the agent can run a dev server,
// poll its output with bash_output, and kill it cleanly when done
// instead of fishing through /tmp logs and dispatching `kill -9`
// with no acknowledgement.
//
// Concurrency: one ProcessManager per shell. Methods are goroutine-
// safe under mu; the per-process reader goroutines append into the
// ring buffer under its own mutex.
//
// Lifecycle: ProcessManager.KillAll terminates running processes;
// ProcessManager.Close (a strict superset) also shuts down the
// background reap goroutine. Shell shutdown should call Close so
// the reaper doesn't outlive its manager.
type ProcessManager struct {
	workspace ProcessWorkspace
	sandbox   Sandboxer
	env       map[string]string
	maxAlive  int
	maxBuffer int
	reapAfter time.Duration

	mu    sync.RWMutex
	procs map[string]*managedProcess
	// pending counts slots reserved by an in-flight StartProcess that
	// has passed the cap check but hasn't yet inserted its process into
	// procs. It's added to the live count so two concurrent starts can't
	// both squeak past maxAlive in the fork-shaped window between the
	// check and the insert.
	pending int
	idGen   func() string // overridable for tests

	// closeCh signals the reapLoop goroutine to exit. closed by
	// Close; nil for managers that haven't been Close'd (the
	// goroutine still gets reclaimed when the manager itself is
	// GC'd, but that's process-exit-only).
	closeCh   chan struct{}
	closeOnce sync.Once
	reapDone  chan struct{} // closed when reapLoop returns
}

// ProcessWorkspace is the minimum surface from code.Workspace the
// manager needs. Defined locally so consumers can pass a fake in
// tests without pulling the whole workspace package.
type ProcessWorkspace interface {
	Root() string
}

// ProcessInfo is the public snapshot of a managed process. Returned
// by List and embedded in tool-result payloads.
type ProcessInfo struct {
	ID          string    `json:"process_id"`
	Command     string    `json:"command"`
	PID         int       `json:"pid"`
	CWD         string    `json:"cwd"`
	StartedAt   time.Time `json:"started_at"`
	Running     bool      `json:"running"`
	ExitedAt    time.Time `json:"exited_at,omitempty"`
	ExitCode    int       `json:"exit_code,omitempty"`
	StdoutLines int       `json:"stdout_lines"`
	StderrLines int       `json:"stderr_lines"`
}

// OutputSnapshot is the result of one bash_output call: incremental
// stdout/stderr since the supplied cursor plus a fresh cursor for
// the next poll. Dropped counters non-zero mean lines rotated out of
// the ring buffer between reads — the agent uses this signal to
// decide whether to throttle output (or accept a partial view).
type OutputSnapshot struct {
	ID                 string   `json:"process_id"`
	Running            bool     `json:"running"`
	ExitCode           *int     `json:"exit_code,omitempty"`
	Stdout             []string `json:"stdout"`
	Stderr             []string `json:"stderr"`
	StdoutCursor       uint64   `json:"stdout_cursor"`
	StderrCursor       uint64   `json:"stderr_cursor"`
	StdoutDroppedSince uint64   `json:"stdout_dropped_since,omitempty"`
	StderrDroppedSince uint64   `json:"stderr_dropped_since,omitempty"`
}

// managedProcess holds the bookkeeping for one background process.
type managedProcess struct {
	id        string
	cmd       *exec.Cmd
	pid       int
	cwd       string
	command   string
	startedAt time.Time

	stdout *lineRingBuffer
	stderr *lineRingBuffer

	// done is closed once cmd.Wait returns AND both reader
	// goroutines have drained their pipes. exited captures the
	// exit code reported by Wait; both protected by lifecycleMu.
	done         chan struct{}
	lifecycleMu  sync.Mutex
	exitedAt     time.Time
	exitCode     int
	exited       bool
	killSignaled bool
	// reaping is set under lifecycleMu the instant the pipes drain and
	// just BEFORE cmd.Wait() reaps the zombie. The kernel won't recycle
	// the pid (and therefore the pgid we signal with kill -pgid) until
	// the zombie is reaped, so "not reaping" guarantees the pgid is still
	// ours. Every signal dispatch goes through signalGroup, which refuses
	// to fire once reaping is set — closing the window where a SIGKILL
	// could land on an unrelated, pid-recycled process group.
	reaping bool

	// reapAt is set when exited transitions to true; the manager's
	// sweep evicts after now > reapAt.
	reapAt time.Time
}

// NewProcessManager constructs a manager bound to a workspace. Apply
// options to tune caps + reap window; defaults are tuned for the
// zarlcode use case (16 concurrent processes, 10k lines per
// stream, 60s post-exit retention).
func NewProcessManager(ws ProcessWorkspace, opts ...ProcessManagerOption) *ProcessManager {
	m := &ProcessManager{
		workspace: ws,
		maxAlive:  16,
		maxBuffer: 10000,
		reapAfter: 60 * time.Second,
		procs:     make(map[string]*managedProcess),
		idGen:     defaultProcessIDGen,
		closeCh:   make(chan struct{}),
		reapDone:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	go m.reapLoop()
	return m
}

// Close terminates every live process (with the same bounded
// escalation as KillAll) AND tears down the background reaper
// goroutine. Idempotent — safe to call multiple times. Use this
// instead of bare KillAll when the manager itself should not
// outlive the call (almost always the right answer on shell exit).
//
// The reaper goroutine receives on a close channel; after Close
// returns its `reapDone` channel is closed too, so callers can
// observe full teardown by selecting on m.ReaperDone() if needed.
func (m *ProcessManager) Close(ctx context.Context) {
	m.KillAll(ctx)
	m.closeOnce.Do(func() {
		close(m.closeCh)
	})
	// Wait for the reaper to exit so we don't leak the goroutine.
	// Bounded by ctx — Close shouldn't outlive its caller's
	// shutdown budget either.
	select {
	case <-m.reapDone:
	case <-ctx.Done():
	}
}

// ProcessManagerOption tunes the manager at construction.
type ProcessManagerOption func(*ProcessManager)

// WithMaxAliveProcesses caps concurrent live processes. Hit it and
// StartProcess returns ErrTooManyProcesses without forking anything.
func WithMaxAliveProcesses(n int) ProcessManagerOption {
	return func(m *ProcessManager) {
		if n > 0 {
			m.maxAlive = n
		}
	}
}

// WithProcessOutputBuffer sets the per-stream ring buffer cap (in
// lines). Lower values reduce memory pressure on chatty processes;
// higher values let the agent inspect more history.
func WithProcessOutputBuffer(lines int) ProcessManagerOption {
	return func(m *ProcessManager) {
		if lines > 0 {
			m.maxBuffer = lines
		}
	}
}

// WithReapAfter sets how long an exited process stays in the manager
// for post-mortem inspection. Default 60s.
func WithReapAfter(d time.Duration) ProcessManagerOption {
	return func(m *ProcessManager) {
		if d > 0 {
			m.reapAfter = d
		}
	}
}

// WithProcessSandbox confines every background process behind sb —
// the same instance the bash tool gets via WithSandbox, so foreground
// and background commands run under one policy. Nil is a no-op.
func WithProcessSandbox(sb Sandboxer) ProcessManagerOption {
	return func(m *ProcessManager) { m.sandbox = sb }
}

// WithProcessEnv appends child-process environment variables to every managed
// background shell command. Values override the inherited process environment.
func WithProcessEnv(env map[string]string) ProcessManagerOption {
	return func(m *ProcessManager) { m.env = cloneEnvMap(env) }
}

// ErrTooManyProcesses is returned by StartProcess when the live
// count is already at the configured cap.
var ErrTooManyProcesses = errors.New("processmgr: too many live processes")

// ErrProcessNotFound is returned by Output / Kill / Info when the
// process_id doesn't match any tracked process.
var ErrProcessNotFound = errors.New("processmgr: process not found")

// StartProcess spawns the command in the workspace root, captures
// stdout/stderr into ring buffers, and returns the assigned
// process_id. The process is detached from ctx — once started it
// outlives the originating tool call. The manager retains a
// reference until reapAfter elapses past its exit.
//
// command runs through /bin/bash -c (falling back to /bin/sh) to
// match the synchronous bash tool's semantics — same env, same
// shell, same setsid isolation.
func (m *ProcessManager) StartProcess(command string) (string, error) {
	if command == "" {
		return "", errors.New("processmgr: empty command")
	}

	// Reserve a slot under the lock. Counting pending alongside the live
	// processes closes the TOCTOU window: without the reservation, two
	// concurrent callers could both read alive == maxAlive-1, both fork,
	// and both insert — overshooting the cap. On any failure before the
	// process lands in procs we release the reservation.
	m.mu.Lock()
	alive := m.pending
	for _, p := range m.procs {
		if !p.isExited() {
			alive++
		}
	}
	if alive >= m.maxAlive {
		m.mu.Unlock()
		return "", fmt.Errorf("%w (cap %d) — kill an old one with stop_process first", ErrTooManyProcesses, m.maxAlive)
	}
	m.pending++
	m.mu.Unlock()

	releaseReservation := func() {
		m.mu.Lock()
		m.pending--
		m.mu.Unlock()
	}

	cmd := exec.CommandContext(context.Background(), shellPath(), "-c", command)
	cmd.Dir = m.workspace.Root()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // own session — kill -<pid> nukes the whole group
	}
	applyCmdEnv(cmd, m.env)
	if m.sandbox != nil {
		if err := m.sandbox.Sandbox(cmd); err != nil {
			releaseReservation()
			return "", fmt.Errorf("processmgr: sandbox: %w", err)
		}
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		releaseReservation()
		return "", fmt.Errorf("processmgr: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		releaseReservation()
		return "", fmt.Errorf("processmgr: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		releaseReservation()
		return "", fmt.Errorf("processmgr: start: %w", err)
	}

	proc := &managedProcess{
		id:        m.idGen(),
		cmd:       cmd,
		pid:       cmd.Process.Pid,
		cwd:       cmd.Dir,
		command:   command,
		startedAt: time.Now(),
		stdout:    newLineRingBuffer(m.maxBuffer),
		stderr:    newLineRingBuffer(m.maxBuffer),
		done:      make(chan struct{}),
	}

	m.mu.Lock()
	m.pending-- // reservation fulfilled — proc now counts toward the live total
	m.procs[proc.id] = proc
	m.mu.Unlock()

	// Two reader goroutines + one waiter. The waiter blocks on the
	// readers so we can guarantee buffers are fully drained before
	// the process is marked exited (otherwise a fast-exiting child's
	// final output would be missing from bash_output calls that race
	// the exit).
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go drainPipe(stdoutPipe, proc.stdout, &drainWG)
	go drainPipe(stderrPipe, proc.stderr, &drainWG)
	go m.waitAndReap(proc, &drainWG)

	return proc.id, nil
}

// waitAndReap blocks until both pipes drain and cmd.Wait returns,
// then marks the process exited and starts the reap countdown. Runs
// in its own goroutine per process; cleans up after itself.
//
// Order is load-bearing: drainWG.Wait() MUST come before cmd.Wait().
// Per [exec.Cmd.StdoutPipe] / [exec.Cmd.StderrPipe] documentation,
// "Wait will close the pipe after seeing the command exit, so most
// callers need not close it themselves; it is thus incorrect to call
// Wait before all reads from the pipe have completed." Earlier this
// function called Wait first — under timing pressure, Wait closed
// the pipe FD before the drain goroutines had pulled the final
// bytes out of the kernel buffer, and a short-lived child's stdout
// would silently vanish. The `go test -race` run exposed this in
// TestBashOutputLabeled_OutputShape (`stdout_cursor: 0` despite the
// process having exited cleanly).
//
// The natural unblock order: process exits → kernel closes its
// write ends of the pipes → drainPipe readers see EOF and return →
// drainWG.Wait() returns → we call cmd.Wait() to reap the zombie.
// cmd.Wait()'s pipe-close is then a no-op (the readers are done) and
// the captured output is intact.
func (m *ProcessManager) waitAndReap(proc *managedProcess, drainWG *sync.WaitGroup) {
	drainWG.Wait()
	// Latch reaping before Wait: the pipes have hit EOF so the process is
	// already gone, but the pgid stays ours until Wait reaps the zombie.
	// signalGroup reads this flag, so any concurrent Kill either signalled
	// before we got here (pgid still valid) or sees reaping and stands down.
	proc.lifecycleMu.Lock()
	proc.reaping = true
	proc.lifecycleMu.Unlock()

	waitErr := proc.cmd.Wait()
	proc.lifecycleMu.Lock()
	proc.exited = true
	proc.exitedAt = time.Now()
	proc.reapAt = proc.exitedAt.Add(m.reapAfter)
	if waitErr != nil {
		if ee, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			proc.exitCode = ee.ExitCode()
		} else {
			// Non-exit error (pipe closed, etc.). Surface -1 so the
			// agent can tell something went wrong without crashing
			// the manager.
			proc.exitCode = -1
		}
	}
	proc.lifecycleMu.Unlock()
	close(proc.done)
}

// reapLoop sweeps exited processes whose retention window has
// elapsed. Runs every 10s — frequent enough to keep the map size
// bounded under heavy churn, infrequent enough to avoid hot-path
// contention with StartProcess.
func (m *ProcessManager) reapLoop() {
	defer close(m.reapDone)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.closeCh:
			return
		case <-ticker.C:
		}
		now := time.Now()
		m.mu.Lock()
		for id, p := range m.procs {
			if p.isExited() && now.After(p.reapAtRead()) {
				delete(m.procs, id)
			}
		}
		m.mu.Unlock()
	}
}

// Output returns incremental stdout/stderr for the named process.
// stdoutCursor / stderrCursor come from a prior call's snapshot;
// pass 0 on the first call to read from the start. maxLines caps
// the return per stream (0 = no cap).
func (m *ProcessManager) Output(id string, stdoutCursor, stderrCursor uint64, maxLines int) (OutputSnapshot, error) {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if !ok {
		return OutputSnapshot{}, fmt.Errorf("%w: %s", ErrProcessNotFound, id)
	}
	out, outCur, outDrop := proc.stdout.ReadSince(stdoutCursor, maxLines)
	errLn, errCur, errDrop := proc.stderr.ReadSince(stderrCursor, maxLines)
	snap := OutputSnapshot{
		ID:                 id,
		Running:            !proc.isExited(),
		Stdout:             bytesToStrings(out),
		Stderr:             bytesToStrings(errLn),
		StdoutCursor:       outCur,
		StderrCursor:       errCur,
		StdoutDroppedSince: outDrop,
		StderrDroppedSince: errDrop,
	}
	if proc.isExited() {
		code := proc.exitCodeRead()
		snap.ExitCode = &code
	}
	return snap, nil
}

// Kill sends signal to the process. SIGTERM (the default) gives the
// process 5s to exit cleanly before escalating to SIGKILL. SIGINT
// applies a similar two-stage escalation. SIGKILL is immediate.
// Returns the eventual exit code (or -1 if the process didn't exit
// in time).
func (m *ProcessManager) Kill(id string, signal syscall.Signal) (int, error) {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrProcessNotFound, id)
	}
	if proc.isExited() {
		return proc.exitCodeRead(), nil
	}
	proc.lifecycleMu.Lock()
	proc.killSignaled = true
	proc.lifecycleMu.Unlock()

	// Every signal routes through signalGroup, which won't fire once the
	// process is being reaped — so an escalation can't deliver SIGKILL to a
	// pid-recycled group. A false return means the process already exited
	// (proc.done is about to close), so we stop escalating and fall through
	// to the bounded final wait.
	switch signal {
	case syscall.SIGKILL:
		proc.signalGroup(syscall.SIGKILL)
	case syscall.SIGINT:
		if proc.signalGroup(syscall.SIGINT) && !waitFor(proc.done) {
			if proc.signalGroup(syscall.SIGTERM) && !waitFor(proc.done) {
				proc.signalGroup(syscall.SIGKILL)
			}
		}
	default: // SIGTERM
		if proc.signalGroup(syscall.SIGTERM) && !waitFor(proc.done) {
			proc.signalGroup(syscall.SIGKILL)
		}
	}
	// Final wait bounded so a process that ignores SIGKILL (e.g.
	// stuck in uninterruptible disk wait) doesn't wedge the caller
	// forever. -1 from exitCodeRead signals "didn't exit in time"
	// per the doc comment.
	if !waitFor(proc.done) {
		return -1, nil
	}
	return proc.exitCodeRead(), nil
}

// KillAll terminates every live process and waits for them to exit,
// bounded by the supplied context. Use on shell shutdown so orphan
// processes don't leak past the TUI quit.
func (m *ProcessManager) KillAll(ctx context.Context) {
	m.mu.RLock()
	live := make([]*managedProcess, 0, len(m.procs))
	for _, p := range m.procs {
		if !p.isExited() {
			live = append(live, p)
		}
	}
	m.mu.RUnlock()
	if len(live) == 0 {
		return
	}
	// Fire all SIGTERMs first; wait in parallel.
	var wg sync.WaitGroup
	for _, p := range live {
		p.signalGroup(syscall.SIGTERM)
		wg.Add(1)
		go func(pp *managedProcess) {
			defer wg.Done()
			select {
			case <-pp.done:
			case <-ctx.Done():
				pp.signalGroup(syscall.SIGKILL)
				if !waitForOrContext(pp.done, ctx, 5*time.Second) {
					slog.WarnContext(
						ctx,
						"process did not exit after SIGKILL during shutdown",
						"process_id",
						pp.id,
						"pid",
						pp.pid,
						"command",
						pp.command,
					)
				}
			case <-time.After(5 * time.Second):
				pp.signalGroup(syscall.SIGKILL)
				if !waitForOrContext(pp.done, ctx, 5*time.Second) {
					slog.WarnContext(
						ctx,
						"process did not exit after SIGKILL during shutdown",
						"process_id",
						pp.id,
						"pid",
						pp.pid,
						"command",
						pp.command,
					)
				}
			}
		}(p)
	}
	wg.Wait()
}

func waitForOrContext(done <-chan struct{}, ctx context.Context, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// List returns a snapshot of all tracked processes (live + recently
// exited still in the reap window). Ordered newest-first so the
// most relevant process is at the top of the agent's view.
func (m *ProcessManager) List() []ProcessInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ProcessInfo, 0, len(m.procs))
	for _, p := range m.procs {
		stdoutCount, _, _ := p.stdout.Snapshot()
		stderrCount, _, _ := p.stderr.Snapshot()
		info := ProcessInfo{
			ID:          p.id,
			Command:     p.command,
			PID:         p.pid,
			CWD:         p.cwd,
			StartedAt:   p.startedAt,
			Running:     !p.isExited(),
			StdoutLines: stdoutCount,
			StderrLines: stderrCount,
		}
		if exited, exitedAt, exitCode := p.exitedSnapshot(); exited {
			info.ExitedAt = exitedAt
			info.ExitCode = exitCode
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b ProcessInfo) int { return b.StartedAt.Compare(a.StartedAt) })
	return out
}

// Info returns a single process's snapshot. Useful for /processes
// detail views; the tool layer uses List().
func (m *ProcessManager) Info(id string) (ProcessInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.procs[id]
	if !ok {
		return ProcessInfo{}, fmt.Errorf("%w: %s", ErrProcessNotFound, id)
	}
	stdoutCount, _, _ := p.stdout.Snapshot()
	stderrCount, _, _ := p.stderr.Snapshot()
	info := ProcessInfo{
		ID:          p.id,
		Command:     p.command,
		PID:         p.pid,
		CWD:         p.cwd,
		StartedAt:   p.startedAt,
		Running:     !p.isExited(),
		StdoutLines: stdoutCount,
		StderrLines: stderrCount,
	}
	if exited, exitedAt, exitCode := p.exitedSnapshot(); exited {
		info.ExitedAt = exitedAt
		info.ExitCode = exitCode
	}
	return info, nil
}

// --- helpers ---

func (p *managedProcess) isExited() bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.exited
}

// signalGroup sends sig to the process's group (kill -pgid, where pgid
// == pid thanks to Setsid) iff the process hasn't begun reaping. The
// check and the syscall happen together under lifecycleMu, and reaping
// is latched before cmd.Wait() frees the pid — so a true return means
// the pgid was still ours at the instant we signalled. Returns false
// once the process is exiting/reaped, signalling the caller to stop
// escalating rather than risk hitting a pid-recycled group.
func (p *managedProcess) signalGroup(sig syscall.Signal) bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.reaping || p.exited {
		return false
	}
	_ = syscall.Kill(-p.pid, sig)
	return true
}

func (p *managedProcess) exitCodeRead() int {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.exitCode
}

// exitedSnapshot returns a consistent snapshot of the exited flag,
// exit timestamp, and exit code under lifecycleMu. Callers that
// previously called isExited() + bare exitedAt / exitCodeRead()
// should use this single call instead to avoid TOCTOU races.
func (p *managedProcess) exitedSnapshot() (bool, time.Time, int) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.exited, p.exitedAt, p.exitCode
}

// reapAtRead returns reapAt under lifecycleMu so the reapLoop can
// read it without racing waitAndReap's write.
func (p *managedProcess) reapAtRead() time.Time {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.reapAt
}

// maxDrainLineBytes caps how much of a single \n-delimited line we
// retain. A line longer than this is truncated to the cap (with a
// marker) — but, crucially, we keep reading past it. The earlier
// bufio.Scanner stopped the whole loop on bufio.ErrTooLong, which both
// silently dropped every later line AND closed the read end of a pipe a
// still-running process was writing to, handing it EPIPE.
// var, not const, purely so tests can shrink it to exercise the
// truncate-and-continue path without a multi-megabyte fixture. Treat it
// as a constant in production code.
var maxDrainLineBytes = 4 * 1024 * 1024

// drainPipe reads r line-by-line into ring until EOF, then releases the
// WG counter so waitAndReap can synchronize on full drain. It never
// stops early on an over-long line, so a process emitting one giant line
// (a packed bundle, a base64 blob) keeps streaming instead of wedging.
// Strips ANSI escape sequences so the agent sees clean content.
func drainPipe(r io.ReadCloser, ring *lineRingBuffer, wg *sync.WaitGroup) {
	defer wg.Done()
	defer r.Close()
	br := bufio.NewReaderSize(r, 64*1024)
	var (
		line      []byte // accumulates the current logical line
		have      bool   // some bytes buffered for the current line
		truncated bool   // current line already hit the cap
	)
	flush := func() {
		if truncated {
			line = append(line, "…[line truncated]"...)
		}
		ring.Append(ansiRe.ReplaceAll(line, nil))
		line = line[:0]
		have, truncated = false, false
	}
	for {
		// ReadSlice returns data up to and including '\n', or
		// bufio.ErrBufferFull when the line outgrows the buffer before a
		// newline arrives — in which case we accumulate and keep going.
		chunk, err := br.ReadSlice('\n')
		sawNL := false
		if n := len(chunk); n > 0 {
			if chunk[n-1] == '\n' {
				chunk = chunk[:n-1]
				sawNL = true
			}
			have = true
			if !truncated {
				if room := maxDrainLineBytes - len(line); len(chunk) > room {
					line = append(line, chunk[:room]...)
					truncated = true
				} else {
					line = append(line, chunk...)
				}
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if sawNL {
			flush()
		}
		if err != nil {
			if have {
				flush() // final line with no trailing newline
			}
			return
		}
	}
}

// waitFor returns true if done closes before timeout.
func waitFor(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// defaultProcessIDGen produces a short, type-friendly id like
// "bash-a7c2". 8 hex chars from crypto/rand — collision-resistant
// well past the 16-process cap.
func defaultProcessIDGen() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "bash-" + hex.EncodeToString(b[:])
}

func bytesToStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}
