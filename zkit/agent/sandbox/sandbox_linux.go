//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Sandbox wraps prepared commands so they run under its Policy. One
// Sandbox is built per workspace and shared by every command spawner
// (foreground bash, background process manager).
type Sandbox struct {
	policy     Policy
	policyJSON string
}

// New returns a Sandbox enforcing p. It errors when the kernel can't
// enforce the policy (no Landlock) — callers decide whether to run
// unsandboxed with a warning or refuse outright; silently degrading
// to a no-op here would let "sandboxed" runs lie about their safety.
func New(p Policy) (*Sandbox, error) {
	if v := ABIVersion(); v < 1 {
		return nil, fmt.Errorf("sandbox: landlock unavailable (kernel abi %d)", v)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("sandbox: marshal policy: %w", err)
	}
	return &Sandbox{policy: p, policyJSON: string(b)}, nil
}

// Policy returns the grant set this sandbox enforces.
func (s *Sandbox) Policy() Policy { return s.policy }

// Sandbox rewrites cmd to run under this sandbox's policy: the argv is
// re-pointed at /proc/self/exe so ExecShim in the child applies the
// Landlock ruleset before exec'ing the original command, and — when the
// policy denies network — the child is cloned into a fresh user+net
// namespace. Call it after cmd is fully prepared (Path, Args, Dir,
// SysProcAttr) and before Start. The host binary MUST call ExecShim at
// the top of main(), or the "shim" child will run the whole program.
func (s *Sandbox) Sandbox(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		return errors.New("sandbox: command already started")
	}
	if len(cmd.Args) == 0 || cmd.Path == "" {
		return errors.New("sandbox: command has no argv")
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	env = append(env, policyEnv+"="+s.policyJSON)
	cmd.Env = env
	cmd.Args = append([]string{shimArgv0, cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/proc/self/exe"
	if !s.policy.AllowNetwork {
		isolateNetwork(cmd)
	}
	return nil
}

// isolateNetwork puts the child in a fresh user+net namespace pair.
// The new netns has only its own loopback (brought up by the shim) —
// host interfaces, host localhost included, don't exist in it. The
// user namespace maps the current uid/gid to themselves so file
// ownership inside the workspace stays sane.
func isolateNetwork(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	attr := cmd.SysProcAttr
	attr.Cloneflags |= syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
	uid, gid := os.Getuid(), os.Getgid()
	attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}}
	attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}}
	// Unprivileged gid_map writes require setgroups to be denied first;
	// Go's exec does that for us when this is false.
	attr.GidMappingsEnableSetgroups = false
}

// ABIVersion reports the kernel's Landlock ABI level (0 when the LSM
// is absent or disabled). Probed once per process.
var ABIVersion = sync.OnceValue(func() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
})
