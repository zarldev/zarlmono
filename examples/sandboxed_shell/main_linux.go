//go:build linux

// Binary sandboxed_shell demonstrates kernel-enforced workspace confinement.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

func main() {
	sandbox.ExecShim()
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out *os.File) error {
	if sandbox.ABIVersion() < 1 {
		fmt.Fprintln(out, "Landlock is unavailable on this Linux kernel; sandbox demo skipped.")
		return nil
	}
	workspace, err := os.MkdirTemp("", "zkit-sandbox-workspace-")
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	outside, err := os.MkdirTemp("", "zkit-sandbox-outside-")
	if err != nil {
		return fmt.Errorf("create outside directory: %w", err)
	}
	defer os.RemoveAll(outside)

	policy := sandbox.Policy{
		AllowNetwork: true,
		ReadDirs:     []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/etc", "/proc", "/dev"},
		WriteDirs:    []string{workspace},
		WriteFiles:   []string{"/dev/null"},
	}
	box, err := sandbox.New(policy)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	allowed := exec.CommandContext(ctx, "/bin/bash", "-c", "printf confined > inside.txt && cat inside.txt")
	allowed.Dir = workspace
	if err := box.Sandbox(allowed); err != nil {
		return fmt.Errorf("prepare allowed command: %w", err)
	}
	allowedOutput, err := allowed.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run allowed command: %w: %s", err, allowedOutput)
	}
	fmt.Fprintf(out, "workspace write: %s\n", strings.TrimSpace(string(allowedOutput)))

	denied := exec.CommandContext(ctx, "/usr/bin/touch", "escape.txt")
	denied.Dir = outside
	if err := box.Sandbox(denied); err != nil {
		return fmt.Errorf("prepare denied command: %w", err)
	}
	deniedOutput, err := denied.CombinedOutput()
	if err == nil {
		return errors.New("outside-workspace write unexpectedly succeeded")
	}
	if _, statErr := os.Stat(outside + "/escape.txt"); !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("outside file state: %w", statErr)
	}
	fmt.Fprintf(out, "outside write: denied (%s)\n", strings.TrimSpace(string(deniedOutput)))
	return nil
}
