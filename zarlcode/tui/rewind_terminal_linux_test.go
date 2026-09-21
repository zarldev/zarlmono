//go:build linux

package tui_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// This is the M1 terminal gate, not a CLI/provider authentication smoke. The
// stock provider identity is supplied by an in-process fake, never an endpoint
// override. Every user action below enters Bubble Tea through an OS PTY.
func TestRewindTerminalPTY(t *testing.T) {
	f := newBeforeFixture(t)
	var calls atomic.Int32
	f.provider.check = func(context.Context) { calls.Add(1) }
	terminal := startRewindTerminal(t, f)
	terminal.waitText(t, "▏") // first rendered composer, after terminal initialization
	terminal.keys(t, "first terminal prompt\r")
	sourceID := ""
	terminal.wait(t, "first durable settlement", func() bool {
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil || !terminalSettled(t, f, id, "first terminal prompt") {
			return false
		}
		sourceID = id
		return true
	})
	terminal.keys(t, "selected terminal prompt\r")
	terminal.wait(t, "second durable settlement", func() bool {
		return terminalSettled(t, f, sourceID, "selected terminal prompt")
	})
	source, err := f.store.GetSessionResumeState(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	terminal.keys(t, "source draft")
	terminal.waitComposer(t, "source draft")
	terminal.keys(t, "\x12") // Ctrl-R opens the actual transcript browser.
	terminal.waitText(t, "r preview rewind")
	terminal.keys(t, "[r")
	for _, text := range []string{
		"Files were not restored.",
		"Create a new continuation before the selected prompt.",
		"The selected text will be prefilled for editing, not submitted.",
		"enter create",
	} {
		terminal.waitText(t, text)
	}
	terminal.output.clear()
	terminal.keys(t, "\r")
	terminal.waitComposer(t, "selected terminal prompt")
	terminal.waitText(t, "selected terminal prompt")
	childID := ""
	terminal.wait(t, "durable continuation", func() bool {
		id, readErr := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if readErr != nil || id == sourceID {
			return false
		}
		child, readErr := f.store.GetSessionResumeState(t.Context(), id)
		if readErr != nil {
			return false
		}
		text, decodeErr := draft.Decode(child.Session.PendingJSON)
		childID = id
		return decodeErr == nil && text == "selected terminal prompt"
	})
	terminal.stop(t)
	if calls.Load() != 2 || f.ui.ComposerText() != "selected terminal prompt" {
		t.Fatal("apply submitted the prefill or lost selected text")
	}

	// New UI and runner: no in-memory turn/context state is reused on restart.
	restarted := restartTerminalFixture(t, f, childID)
	restarted.provider.check = func(context.Context) { calls.Add(1) }
	var request llm.CompletionRequest
	restarted.provider.request = func(got llm.CompletionRequest) { request = got }
	childTerminal := startRewindTerminal(t, restarted)
	childTerminal.waitText(t, "selected terminal prompt")
	childTerminal.waitText(t, "files were not restored")
	childTerminal.waitComposer(t, "selected terminal prompt")
	if calls.Load() != 2 {
		t.Fatal("restart auto-submitted the prefill")
	}
	childTerminal.keys(t, strings.Repeat("\x7f", len("selected terminal prompt"))+"edited terminal continuation\r")
	childTerminal.wait(t, "restarted continuation settlement", func() bool {
		return terminalSettled(t, restarted, childID, "edited terminal continuation")
	})
	childTerminal.stop(t)
	if calls.Load() != 3 {
		t.Fatal("explicit continuation did not dispatch exactly once")
	}
	var requestText strings.Builder
	for _, message := range request.Messages {
		requestText.WriteString(message.Content)
		requestText.WriteByte('\n')
	}
	if text := requestText.String(); !strings.Contains(text, "first terminal prompt") ||
		strings.Contains(text, "selected terminal prompt") ||
		!strings.Contains(text, "edited terminal continuation") ||
		!strings.Contains(text, rewind.FilesUnchangedNotice) {
		t.Fatal("restarted request lost exact context/warning or retained discarded history")
	}

	original := restartTerminalFixture(t, f, sourceID)
	original.provider.check = func(context.Context) { calls.Add(1) }
	originalTerminal := startRewindTerminal(t, original)
	originalTerminal.waitText(t, "source draft")
	originalTerminal.waitComposer(t, "source draft")
	originalTerminal.keys(t, "\x12")
	originalTerminal.waitText(t, "r preview rewind")
	originalTerminal.waitText(t, "selected terminal prompt")
	originalTerminal.stop(t)
	after, err := f.store.GetSessionResumeState(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.Transcript, after.Transcript) ||
		!reflect.DeepEqual(sourceContext(t, source.Session.ContextJSON), original.live.ContextSnapshot()) || calls.Load() != 3 {
		t.Fatal("original branch history changed or resume dispatched a turn")
	}
}

func terminalSettled(t *testing.T, f *beforeFixture, id, prompt string) bool {
	t.Helper()
	saved, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		return false
	}
	head, err := rewind.DecodeResume(saved.Session.ContextJSON)
	if err != nil || head.SettledTurnID == "" || head.EventWatermark != saved.Transcript.Revision {
		return false
	}
	for i := len(head.Context) - 1; i >= 0; i-- {
		if head.Context[i].Role == llm.RoleUser {
			return head.Context[i].Content == prompt
		}
	}
	return false
}

func restartTerminalFixture(t *testing.T, source *beforeFixture, id string) *beforeFixture {
	t.Helper()
	f := &beforeFixture{store: source.store, ws: source.ws, provider: &beforeProvider{}}
	f.sink = teasink.New(func(tea.Msg) {})
	t.Cleanup(f.sink.Close)
	f.live = engine.NewLiveRunner(f.provider, f.ws, "saved-model", engine.WithLiveSink(f.sink))
	f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	testCtx := t.Context()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), 5*time.Second)
		defer cancel()
		if err := f.live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	f.ui = tui.New()
	f.ui.SetLiveRunner(f.live)
	f.ui.SetLiveEventSink(f.sink)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil // only the explicitly composed fake route is available
	f.ui.SetSettings(settings)
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	return f
}

// Probes only read the composer on the event-loop goroutine. They neither
// inject user actions nor call Update/View concurrently with the real program.
type terminalComposerProbe chan string

type rewindTerminalModel struct{ *tui.UI }

func (m rewindTerminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if probe, ok := msg.(terminalComposerProbe); ok {
		probe <- m.ComposerText()
		return m, nil
	}
	_, cmd := m.UI.Update(msg)
	return m, cmd
}

type rewindTerminal struct {
	master  *os.File
	program *tea.Program
	done    chan struct{}
	runErr  error // read only after done closes
	output  terminalCapture
	stopped bool
	ui      *tui.UI
}

// Keep diagnostics bounded and never print captured prompt/context bytes.
type terminalCapture struct {
	mu   sync.Mutex
	data []byte
}

func (c *terminalCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, p...)
	const limit = 256 * 1024
	if len(c.data) > limit {
		c.data = c.data[len(c.data)-limit:]
	}
	return len(p), nil
}

func (c *terminalCapture) contains(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(ansi.Strip(string(c.data)), text)
}

func (c *terminalCapture) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = nil
}

func startRewindTerminal(t *testing.T, f *beforeFixture) *rewindTerminal {
	t.Helper()
	// O_NONBLOCK lets os.File use its poller so Close interrupts the owned reader.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open isolated PTY (terminal gate unavailable): %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 120, Row: 42}); err != nil {
		t.Fatal(err)
	}
	terminal := &rewindTerminal{master: master, done: make(chan struct{}), ui: f.ui}
	readerDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&terminal.output, master)
		readerDone <- copyErr
	}()
	t.Cleanup(func() {
		_ = master.Close()
		if err := <-readerDone; err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, unix.EIO) {
			t.Errorf("read isolated PTY: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	terminal.program = tea.NewProgram(rewindTerminalModel{f.ui},
		tea.WithContext(ctx), tea.WithInput(slave), tea.WithOutput(slave),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "LANG=C.UTF-8"}),
		tea.WithWindowSize(120, 42), tea.WithoutSignalHandler())
	f.sink.SetSend(terminal.program.Send)
	go func() {
		_, terminal.runErr = terminal.program.Run()
		close(terminal.done)
	}()
	t.Cleanup(func() {
		if !terminal.stopped {
			terminal.program.Kill()
			<-terminal.done
			terminal.flush(t)
		}
	})
	return terminal
}

func (p *rewindTerminal) keys(t *testing.T, keys string) {
	t.Helper()
	if _, err := io.WriteString(p.master, keys); err != nil {
		t.Fatalf("write isolated terminal keys: %v", err)
	}
}

func (p *rewindTerminal) waitText(t *testing.T, text string) {
	t.Helper()
	p.wait(t, "terminal text: "+text, func() bool { return p.output.contains(text) })
}

func (p *rewindTerminal) waitComposer(t *testing.T, want string) {
	t.Helper()
	p.wait(t, "composer prefill/edit", func() bool {
		probe := make(terminalComposerProbe, 1)
		p.program.Send(probe)
		select {
		case got := <-probe:
			return got == want
		case <-p.done:
			return false
		}
	})
}

func (p *rewindTerminal) wait(t *testing.T, label string, ready func() bool) {
	t.Helper()
	// PTY and SQLite I/O are outside synctest's clock. Poll observable state,
	// bounded by both a phase deadline and the program's lifetime context.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s (terminal transcript withheld)", label)
		case <-p.done:
			t.Fatalf("terminal exited waiting for %s: %v", label, p.runErr)
		}
	}
}

func (p *rewindTerminal) stop(t *testing.T) {
	t.Helper()
	p.program.Quit()
	<-p.done
	p.stopped = true
	if p.runErr != nil {
		t.Fatalf("run isolated terminal: %v", p.runErr)
	}
	p.flush(t)
}

func (p *rewindTerminal) flush(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	if err := p.ui.FlushSessionPersistence(ctx); err != nil {
		t.Errorf("flush terminal session: %v", err)
	}
}
