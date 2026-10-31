package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/zexec"
)

type composeCommand struct {
	binary string
	prefix []string
	label  string
}

const runtimeOwnerFile = "compose-runtime"

var composeRuntimes = []composeCommand{
	{binary: "docker", prefix: []string{"compose"}, label: "docker compose"},
	{binary: "podman", prefix: []string{"compose"}, label: "podman compose"},
}

// resolveCompose chooses a runtime only for an as-yet unmanaged service.
func resolveCompose(ctx context.Context) (composeCommand, error) {
	for _, candidate := range composeRuntimes {
		if err := probeCompose(ctx, candidate); err == nil {
			return candidate, nil
		}
		if cause := context.Cause(ctx); cause != nil {
			return composeCommand{}, cause
		}
	}
	return composeCommand{}, errors.New("no Compose runtime available; install Docker Compose or Podman with a Compose provider such as podman-compose")
}

func probeCompose(ctx context.Context, candidate composeCommand) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := append(append([]string(nil), candidate.prefix...), "version")
	err := newComposeCommand(probeCtx, candidate.binary, args...).Run()
	return errors.Join(err, context.Cause(probeCtx))
}

// serviceCompose preserves the managed project's runtime even when another
// implementation becomes available later. An unavailable owner fails closed.
func serviceCompose(ctx context.Context, dir string) (composeCommand, error) {
	owner, err := os.ReadFile(filepath.Join(dir, runtimeOwnerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return resolveCompose(ctx)
	}
	if err != nil {
		return composeCommand{}, fmt.Errorf("read service runtime owner: %w", err)
	}
	for _, candidate := range composeRuntimes {
		if strings.TrimSpace(string(owner)) != candidate.binary {
			continue
		}
		if err := probeCompose(ctx, candidate); err != nil {
			return candidate, fmt.Errorf("recorded %s unavailable: %w", candidate.label, err)
		}
		return candidate, nil
	}
	return composeCommand{}, errors.New("invalid service runtime owner; expected docker or podman in compose-runtime")
}

// Cancellation owns the CLI process group, including Podman's Compose provider.
// Detached service containers are intentionally owned by the Compose project.
func newComposeCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	zexec.StartProcessGroup(cmd)
	cmd.Cancel = func() error { return zexec.KillProcessGroup(cmd) }
	cmd.WaitDelay = time.Second
	return cmd
}
