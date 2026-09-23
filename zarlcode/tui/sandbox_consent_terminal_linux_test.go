//go:build linux

package tui_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSandboxConsentTerminalInput(t *testing.T) {
	for _, tt := range []struct {
		name, keys string
		accepted   bool
	}{
		{name: "explicit yes", keys: "y", accepted: true},
		{name: "default decline", keys: "\r"},
		{name: "paste cannot accept", keys: "\x1b[200~y\x1b[201~\r"},
		{name: "control c exits", keys: "\x03"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			master, slave := consentTerminalPair(t)
			var output terminalCapture
			readerDone := make(chan error, 1)
			go func() {
				_, err := io.Copy(&output, master)
				readerDone <- err
			}()
			t.Cleanup(func() {
				_ = master.Close()
				if err := <-readerDone; err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, unix.EIO) {
					t.Errorf("read consent terminal: %v", err)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			model := &tui.SandboxConsent{}
			program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(slave), tea.WithOutput(slave),
				tea.WithEnvironment([]string{"TERM=xterm-256color", "LANG=C.UTF-8"}),
				tea.WithWindowSize(80, 24), tea.WithoutSignalHandler())
			done := make(chan struct{})
			var runErr error
			go func() {
				_, runErr = program.Run()
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for !output.contains("remember for this workspace") {
				select {
				case <-ticker.C:
				case <-done:
					t.Fatalf("consent exited before displaying its warning: %v", runErr)
				case <-ctx.Done():
					t.Fatal("consent warning did not render")
				}
			}
			if _, err := io.WriteString(master, tt.keys); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				if runErr != nil || model.Accepted() != tt.accepted {
					t.Fatalf("accepted=%v err=%v", model.Accepted(), runErr)
				}
			case <-ctx.Done():
				t.Fatal("consent did not terminate after terminal decision")
			}
		})
	}
}

func consentTerminalPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
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
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 80, Row: 24}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}
