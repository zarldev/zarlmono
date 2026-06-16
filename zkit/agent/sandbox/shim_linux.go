//go:build linux

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/landlock-lsm/go-landlock/landlock"
	"golang.org/x/sys/unix"
)

// ExecShim is the child half of the sandbox. Call it FIRST in main()
// of every binary that wires a Sandbox — before flag parsing, before
// logging, before anything that could observe the process. When the
// process was started by Sandbox.Sandbox it never returns: it applies
// the policy from the environment and execs the real command in place
// (exiting 125 if the policy can't be enforced — fail closed, never
// run the command unconfined). In a normal launch it's a no-op.
func ExecShim() {
	policyJSON := os.Getenv(policyEnv)
	if policyJSON == "" {
		return
	}
	os.Exit(runShim(policyJSON, os.Args[1:]))
}

// runShim enforces the policy and execs argv. Split from ExecShim so
// tests can drive it without re-exec gymnastics. Returns an exit code
// instead of erroring: by the time the shim runs there is no parent
// Go frame to return to, only a process status.
func runShim(policyJSON string, argv []string) int {
	var p Policy
	if err := json.Unmarshal([]byte(policyJSON), &p); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: decode policy: %v\n", err)
		return 125
	}
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "sandbox shim: no command to exec")
		return 125
	}
	if !p.AllowNetwork {
		// Best-effort: the namespace already has no route anywhere; a
		// downed lo only breaks commands that wanted 127.0.0.1 within
		// their own run, it never widens access.
		if err := upLoopback(); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox shim: loopback up: %v\n", err)
		}
	}
	if err := restrict(p); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: landlock: %v\n", err)
		return 125
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, policyEnv+"=")
	})
	if err := unix.Exec(argv[0], argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: exec %s: %v\n", argv[0], err)
		return 126
	}
	return 0 // unreachable: Exec replaced the image
}

// restrict applies the policy as a Landlock ruleset on the current
// process; the restriction is inherited across the subsequent exec.
// V3 matches what the fleet kernels enforce (WSL2 6.6); BestEffort
// downgrades on older ABIs rather than refusing — the parent already
// verified ABI >= 1 before wiring the sandbox at all.
//
// WithRefer on the write grants matters: without it Landlock denies
// cross-directory rename/link inside the granted trees, which breaks
// git (object files move tmp -> objects/) and go build.
func restrict(p Policy) error {
	var rules []landlock.Rule
	if len(p.ReadDirs) > 0 {
		rules = append(rules, landlock.RODirs(p.ReadDirs...).IgnoreIfMissing())
	}
	if len(p.ReadFiles) > 0 {
		rules = append(rules, landlock.ROFiles(p.ReadFiles...).IgnoreIfMissing())
	}
	if len(p.WriteDirs) > 0 {
		rules = append(rules, landlock.RWDirs(p.WriteDirs...).WithRefer().IgnoreIfMissing())
	}
	if len(p.WriteFiles) > 0 {
		rules = append(rules, landlock.RWFiles(p.WriteFiles...).IgnoreIfMissing())
	}
	return landlock.V3.BestEffort().RestrictPaths(rules...)
}

// upLoopback brings up lo inside the fresh network namespace via the
// classic SIOCSIFFLAGS ioctl — no netlink dependency for one bit flip.
// The userns creator holds CAP_NET_ADMIN over its own netns, so this
// works unprivileged.
func upLoopback() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer unix.Close(fd)
	req, err := unix.NewIfreq("lo")
	if err != nil {
		return fmt.Errorf("ifreq lo: %w", err)
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, req); err != nil {
		return fmt.Errorf("get lo flags: %w", err)
	}
	req.SetUint16(req.Uint16() | unix.IFF_UP | unix.IFF_RUNNING)
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, req); err != nil {
		return fmt.Errorf("set lo up: %w", err)
	}
	return nil
}
