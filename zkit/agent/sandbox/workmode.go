package sandbox

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// WorkModeSandbox selects a kernel policy from the spawned task's work mode.
// Verify commands use verify; all other commands use normal. Both policies must
// be independently enforceable Sandbox instances.
type WorkModeSandbox struct {
	normal *Sandbox
	verify *Sandbox
}

// NewWorkModeSandbox constructs mode-aware shell confinement. A nil policy is
// rejected by construction because silently weakening one mode would make the
// boundary misleading.
func NewWorkModeSandbox(normal, verify *Sandbox) *WorkModeSandbox {
	if normal == nil || verify == nil {
		return nil
	}
	return &WorkModeSandbox{normal: normal, verify: verify}
}

// Sandbox applies the normal coding policy for callers without task context.
func (s *WorkModeSandbox) Sandbox(cmd *exec.Cmd) error {
	return s.normal.Sandbox(cmd)
}

// SandboxContext applies the read-only verify policy to verify-mode calls.
func (s *WorkModeSandbox) SandboxContext(ctx context.Context, cmd *exec.Cmd) error {
	if taskscope.WorkModeFrom(ctx) == taskscope.WorkModes.VERIFY {
		return s.verify.Sandbox(cmd)
	}
	return s.normal.Sandbox(cmd)
}

// VerifyPolicy derives a verify-mode policy by removing write access to the
// workspace while retaining temp/cache write grants required by compilers and
// test runners. workspaceRoot is matched after filepath normalization.
func VerifyPolicy(normal Policy, workspaceRoot string) Policy {
	workspaceRoot = cleanPolicyPath(workspaceRoot)
	verify := normal
	verify.WriteDirs = append([]string(nil), normal.WriteDirs...)
	verify.WriteFiles = append([]string(nil), normal.WriteFiles...)
	verify.ReadDirs = append([]string(nil), normal.ReadDirs...)
	verify.ReadFiles = append([]string(nil), normal.ReadFiles...)
	verify.WriteDirs = filterPolicyPath(verify.WriteDirs, workspaceRoot)
	verify.ReadDirs = append(verify.ReadDirs, workspaceRoot)
	return verify
}

func cleanPolicyPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func filterPolicyPath(paths []string, denied string) []string {
	if denied == "" {
		return paths
	}
	kept := paths[:0]
	for _, path := range paths {
		grant := cleanPolicyPath(path)
		if grant != denied && !pathWithin(denied, grant) {
			kept = append(kept, path)
		}
	}
	return kept
}

func pathWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
