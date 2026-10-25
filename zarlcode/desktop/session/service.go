// Package session connects the native frontend to the existing application
// composition and serialized session controller. It creates no second agent.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/zapp"
)

const snapshotAction = "snapshot"

// ConnectOptions contains only user-selected startup inputs. Passphrase is
// consumed during connection, never logged or returned in a snapshot.
type ConnectOptions struct {
	Passphrase      string `json:"passphrase"`
	Resume          bool   `json:"resume"`
	AllowUnconfined bool   `json:"allowUnconfined"`
}

// Service owns one live workspace session and its goroutine. Shutdown joins
// the controller before closing provider, sink, process manager and storage.
type Service struct {
	mu             sync.Mutex
	workbench      *tui.Workbench
	done           chan struct{}
	closing        bool
	err            error
	closeConfirmed bool
}

// New creates a disconnected service; Connect opens the configured workspace.
func New() *Service { return &Service{} }

// ErrUnsaved marks a settled session whose latest state is not durable.
var ErrUnsaved = errors.New("session has unsaved changes")

// Connect opens the same configured provider, tools, settings and persistence
// as the TUI, without terminal prompts or silently granting sandbox consent.
func (s *Service) Connect(options ConnectOptions) (tui.WorkbenchSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return tui.WorkbenchSnapshot{}, errors.New("workbench is closing")
	}
	if s.workbench != nil {
		return s.workbench.Request(context.Background(), tui.WorkbenchCommand{Action: snapshotAction})
	}
	if len(options.Passphrase) > 4096 {
		return tui.WorkbenchSnapshot{}, errors.New("passphrase too long")
	}
	ctx, cancel := context.WithCancel(context.Background())
	launch := tui.Launch{Resume: options.Resume, PromptProfile: engine.PromptProfiles.LEAN, Workbench: &tui.WorkbenchStartup{
		Passphrase: func(setup, retry bool) (string, error) {
			if setup {
				return "", errors.New("configure credentials before opening the workbench")
			}
			if retry {
				return "", errors.New("incorrect vault passphrase")
			}
			if options.Passphrase == "" {
				return "", errors.New("vault passphrase required")
			}
			return options.Passphrase, nil
		},
		ConfirmUnconfined: func(context.Context) (bool, error) {
			if !options.AllowUnconfined {
				return false, errors.New("shell confinement is unavailable: explicit unconfined-shell consent required")
			}
			return true, nil
		},
	}}
	app := zapp.New[*tui.Zarlcode](launch)
	instance, err := launch.Create(ctx, app)
	options.Passphrase = ""
	if err != nil {
		cancel()
		closeErr := app.Close(context.Background())
		return tui.WorkbenchSnapshot{}, errors.Join(err, closeErr)
	}
	w := instance.NewWorkbench(ctx)
	s.workbench = w
	s.done = make(chan struct{})
	done := s.done
	go func() {
		runErr := w.Run(context.Background())
		cancel()
		closeErr := app.Close(context.Background())
		s.mu.Lock()
		s.err = errors.Join(runErr, closeErr)
		s.mu.Unlock()
		close(done)
	}()
	return w.Request(ctx, tui.WorkbenchCommand{Action: snapshotAction})
}

// Command applies a validated domain action through the shared session owner.
// It does not expose direct filesystem, shell, vault or database methods.
func (s *Service) Command(command tui.WorkbenchCommand) (tui.WorkbenchSnapshot, error) {
	s.mu.Lock()
	w, closing, runErr := s.workbench, s.closing, s.err
	defer s.mu.Unlock()
	if closing {
		return tui.WorkbenchSnapshot{}, errors.New("workbench is closing")
	}
	if runErr != nil {
		return tui.WorkbenchSnapshot{}, fmt.Errorf("session stopped: %w", runErr)
	}
	if w == nil {
		return tui.WorkbenchSnapshot{}, errors.New("connect a session first")
	}
	return w.Request(context.Background(), command)
}

// CheckClose refuses to hide a live or unsaved session. Active work is asked
// to stop while its owner remains available to apply events and save results.
//
//wails:ignore
func (s *Service) CheckClose() error {
	s.mu.Lock()
	w := s.workbench
	confirmed := s.closeConfirmed
	s.mu.Unlock()
	if confirmed || w == nil {
		return nil
	}
	snapshot, err := w.Request(context.Background(), tui.WorkbenchCommand{Action: snapshotAction})
	if err != nil {
		return err
	}
	switch snapshot.State {
	case "ready":
		return nil
	case "unsaved":
		return ErrUnsaved
	default:
		_, err = w.Request(context.Background(), tui.WorkbenchCommand{Action: "stop"})
		if err != nil {
			return err
		}
		return errors.New("stopping; close again once the session is saved")
	}
}

// ConfirmCloseWithoutSaving permits native quit only after explicit user consent
// and only for a settled unsaved session. It never overwrites a checkpoint.
//
//wails:ignore
func (s *Service) ConfirmCloseWithoutSaving() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workbench == nil {
		return errors.New("session is not ready for unsaved close")
	}
	snapshot, err := s.workbench.Request(context.Background(), tui.WorkbenchCommand{Action: snapshotAction})
	if err != nil {
		return err
	}
	if snapshot.State != "unsaved" {
		return errors.New("session is not ready for unsaved close")
	}
	s.closeConfirmed = true
	s.closing = true
	return nil
}

// Shutdown stops admission and joins all owned work before returning. It is
// called by the native application lifecycle, never via generated JS bindings.
//
//wails:ignore
func (s *Service) Shutdown() error {
	s.mu.Lock()
	s.closing = true
	w, done := s.workbench, s.done
	s.mu.Unlock()
	if w != nil {
		w.Close()
		<-done
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
