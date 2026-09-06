// Package tuismoke exercises onboarding and credential persistence in an isolated
// real terminal. It owns the tmux server and all temporary application state.
package tuismoke

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const passphrase = "smoke-passphrase"

// Run builds the app (or uses binary when provided), exercises its real terminal
// setup/save/restart/unlock flow, and cleans up on success, failure, or cancellation.
// root is the repository root. timeout bounds each UI transition, not the build.
func Run(ctx context.Context, root, binary string, timeout time.Duration, output io.Writer) (err error) {
	if timeout <= 0 {
		return errors.New("tui smoke: timeout must be positive")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository: %w", err)
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tui smoke requires tmux: %w", err)
	}
	dir, err := os.MkdirTemp("", "zarlcode-tui-smoke-")
	if err != nil {
		return fmt.Errorf("create isolated state: %w", err)
	}
	h := harness{tmux: tmux, dir: dir, timeout: timeout}
	defer func() { err = errors.Join(err, h.close(ctx)) }()
	for _, name := range []string{"home", "workspace"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}
	if binary == "" {
		binary = filepath.Join(dir, "zarlcode")
		buildCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd")
		cmd.Dir = filepath.Join(root, "zarlcode")
		cmd.WaitDelay = 2 * time.Second
		if data, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build smoke binary: %w\n%s", err, data)
		}
	}
	h.binary, err = filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("resolve binary: %w", err)
	}
	info, err := os.Stat(h.binary)
	if err != nil {
		return fmt.Errorf("stat binary: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("tui smoke: binary must be executable")
	}
	if err := h.walkthrough(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "tui smoke: onboarding, in-TUI encrypted credential setup/unlock, resize, help, quit, and shutdown passed")
	return err
}

type harness struct {
	tmux    string
	dir     string
	binary  string
	session string
	timeout time.Duration
}

func (h *harness) command(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	all := append([]string{"-S", filepath.Join(h.dir, "tmux.sock"), "-f", os.DevNull}, args...)
	cmd := exec.CommandContext(ctx, h.tmux, all...) //nolint:gosec // tmux is resolved by LookPath; arguments are owned by this local test harness, never a shell.
	cmd.WaitDelay = time.Second
	cmd.Env = isolatedEnv(filepath.Join(h.dir, "home"))
	data, err := cmd.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return string(data), nil
}

func isolatedEnv(home string) []string {
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "TMUX", "TMUX_PANE", "TERM":
			continue
		}
		env = append(env, value)
	}
	return append(env, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "TERM=xterm-256color")
}

func (h *harness) start(ctx context.Context, session string) error {
	h.session = session
	// tmux uses execvp rather than a shell when passed multiple command arguments.
	_, err := h.command(ctx, "new-session", "-d", "-x", "80", "-y", "24", "-c", filepath.Join(h.dir, "workspace"),
		"-s", session, "env", "HOME="+filepath.Join(h.dir, "home"), "TERM=xterm-256color", h.binary)
	return err
}

func (h *harness) keys(ctx context.Context, keys ...string) error {
	_, err := h.command(ctx, append([]string{"send-keys", "-t", h.session}, keys...)...)
	return err
}

func (h *harness) wait(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var screen string
	for {
		capture, err := h.command(ctx, "capture-pane", "-p", "-t", h.session+":0.0")
		if err == nil {
			screen = capture
			if strings.Contains(screen, text) {
				return screen, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for %q: %w\n%s", text, ctx.Err(), screen)
		case <-ticker.C:
		}
	}
}

func (h *harness) step(ctx context.Context, text string, keys ...string) error {
	if err := h.keys(ctx, keys...); err != nil {
		return err
	}
	_, err := h.wait(ctx, text)
	return err
}

func (h *harness) quit(ctx context.Context) error {
	if err := h.step(ctx, "quit ƶarl/code?", "C-c"); err != nil {
		return err
	}
	if err := h.keys(ctx, "Enter"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := h.command(ctx, "has-session", "-t", h.session)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("wait for %s shutdown: %w", h.session, ctx.Err())
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return nil // tmux reports an absent session/server with status 1.
			}
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s shutdown: %w", h.session, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (h *harness) close(ctx context.Context) error {
	// Cleanup outlives cancellation of the walkthrough, but is independently bounded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := h.command(ctx, "kill-server")
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		err = nil // No server was started, or it exited with its last session.
	}
	return errors.Join(err, os.RemoveAll(h.dir))
}

// VerifyCredentialStorage checks current encrypted storage without creating or
// migrating the database. It never includes credential bytes in diagnostics.
func VerifyCredentialStorage(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	store, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return fmt.Errorf("open smoke database: %w", err)
	}
	defer store.Close()
	var ciphertext []byte
	var storage string
	var version int
	err = store.QueryRowContext(ctx, "SELECT ciphertext, storage, key_version FROM api_keys WHERE workspace = '' AND provider = 'openai'").Scan(&ciphertext, &storage, &version)
	if err != nil {
		return fmt.Errorf("read saved credential: %w", err)
	}
	if storage != "vault" || version != 2 || len(ciphertext) == 0 || bytes.Equal(ciphertext, []byte("smoke-secret")) {
		return errors.New("credential was not saved with the current encrypted format")
	}
	return nil
}
