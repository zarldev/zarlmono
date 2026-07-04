// Package sandbox confines shell commands spawned by the agent's tools
// behind kernel-enforced boundaries: a Landlock filesystem allow-list
// plus (optionally) an empty network namespace. The existing guardrails
// are policy — they parse and veto commands before they run. This layer
// is mechanism: once a command is running, the kernel denies whatever
// the policy didn't grant, no matter what the command turned out to do.
//
// # How it works
//
// Landlock rules can only be applied to the calling process (and then
// inherited across exec), so the sandbox re-executes the current binary
// as a short-lived shim: [Sandbox.Sandbox] rewrites a prepared
// [os/exec.Cmd] to run /proc/self/exe with the serialized [Policy] in
// the environment, and [ExecShim] — which every wired binary must call
// first thing in main() — detects that marker in the child, applies the
// Landlock ruleset, and execs the original argv in place. Network
// denial doesn't need the shim: it's a user+net namespace pair set on
// the command's SysProcAttr at clone time (the shim only brings up
// loopback inside the new namespace).
//
// # What the default policy grants
//
// [DefaultPolicy] is calibrated for a coding agent: read+execute on the
// system directories, read on git's global config, and write only to
// the workspace, the temp directories, and the toolchain caches that
// builds need (~/.cache, the Go module cache). Everything else —
// notably ~/.ssh, ~/.aws, ~/.zarlcode and the rest of $HOME — is not
// granted and therefore invisible to the command. Landlock has no deny
// rules; secrecy falls out of the allow-list being narrow.
package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// policyEnv carries the serialized Policy from the parent into the
// re-exec'd shim child. ExecShim strips it before the real command
// runs, which also breaks recursion when the sandboxed command is
// this binary itself.
const policyEnv = "ZK_SANDBOX_POLICY"

// shimArgv0 is the argv[0] the shim child is started with — purely
// cosmetic (ps/diagnostics); detection uses policyEnv.
const shimArgv0 = "zk-sandbox-shim"

// Env knobs honoured by DefaultPolicy: colon-separated extra paths to
// grant, and the master switch read by EnabledFromEnv.
const (
	envExtraRO = "ZK_SANDBOX_RO"
	envExtraRW = "ZK_SANDBOX_RW"
	envSwitch  = "ZARLCODE_SANDBOX"
)

// Policy is the access grant set for one sandboxed command. Landlock
// is an allow-list: anything not reachable through these grants is
// denied, including visibility (stat/readdir).
type Policy struct {
	// ReadDirs are directory trees granted read + execute.
	ReadDirs []string `json:"read_dirs,omitempty"`
	// ReadFiles are individual files granted read (no directory access).
	ReadFiles []string `json:"read_files,omitempty"`
	// WriteDirs are directory trees granted full read/write/execute,
	// including cross-directory rename/link within the grant.
	WriteDirs []string `json:"write_dirs,omitempty"`
	// WriteFiles are individual files granted read+write — device
	// nodes like /dev/null that live inside read-only trees.
	WriteFiles []string `json:"write_files,omitempty"`
	// AllowNetwork keeps the command in the host network namespace.
	// When false the command runs in a fresh user+net namespace whose
	// only interface is its own loopback — no host services, no
	// internet, not even the host's 127.0.0.1.
	AllowNetwork bool `json:"allow_network"`
}

// DefaultPolicy returns the standard coding-agent grant set rooted at
// the workspace. Network is allowed — local toolchains fetch modules
// and tests talk to localhost services; flip AllowNetwork off for
// runs that shouldn't reach anything.
//
// Extra grants come from the ZK_SANDBOX_RO / ZK_SANDBOX_RW env vars
// (colon-separated paths) so a workspace with an unusual toolchain
// can widen the policy without a code change. Missing paths are fine:
// every grant is applied with ignore-if-missing semantics.
func DefaultPolicy(workspaceRoot string) Policy {
	p := Policy{
		AllowNetwork: true,
		ReadDirs: []string{
			"/usr", "/bin", "/sbin",
			"/lib", "/lib32", "/lib64",
			"/etc", "/opt", "/run", "/srv", "/var",
			"/proc", "/sys", "/dev",
		},
		WriteDirs: []string{
			workspaceRoot,
			"/tmp", "/var/tmp", "/dev/shm", "/dev/pts",
		},
		WriteFiles: []string{"/dev/null", "/dev/zero", "/dev/ptmx", "/dev/tty"},
	}
	// WSL commonly symlinks /etc/resolv.conf to /mnt/wsl/resolv.conf. Without a
	// read grant to that target, sandboxed processes can reach the network but
	// cannot resolve hostnames because libc/Go DNS config loading dies on the
	// symlink target. Grant just the resolver file, not all of /mnt.
	if _, err := os.Stat("/mnt/wsl/resolv.conf"); err == nil {
		p.ReadFiles = append(p.ReadFiles, "/mnt/wsl/resolv.conf")
	}
	if tmp := os.TempDir(); tmp != "/tmp" {
		p.WriteDirs = append(p.WriteDirs, tmp)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		p.ReadDirs = append(p.ReadDirs,
			filepath.Join(home, ".config", "git"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
		)
		p.ReadFiles = append(p.ReadFiles, filepath.Join(home, ".gitconfig"))
		// Toolchain caches: builds are unusable without them. GOCACHE
		// defaults under ~/.cache, which is granted wholesale (npm, pip,
		// uv and friends all cache there too).
		p.WriteDirs = append(p.WriteDirs, filepath.Join(home, ".cache"))
		modCache := os.Getenv("GOMODCACHE")
		if modCache == "" {
			if gopath := os.Getenv("GOPATH"); gopath != "" {
				modCache = filepath.Join(gopath, "pkg", "mod")
			} else {
				modCache = filepath.Join(home, "go", "pkg", "mod")
			}
		}
		p.WriteDirs = append(p.WriteDirs, modCache)
	}
	if cache := os.Getenv("GOCACHE"); cache != "" {
		p.WriteDirs = append(p.WriteDirs, cache)
	}
	// A linked git worktree keeps its real .git directory inside the
	// main repository — git inside the workspace dies without it.
	if common := gitCommonDir(workspaceRoot); common != "" {
		p.WriteDirs = append(p.WriteDirs, common)
	}
	p.ReadDirs = append(p.ReadDirs, splitPathList(os.Getenv(envExtraRO))...)
	p.WriteDirs = append(p.WriteDirs, splitPathList(os.Getenv(envExtraRW))...)
	return p
}

// WithExecPath augments p so the sandbox can execute a binary located at path.
// It grants read access to the file itself plus read+execute traversal on each
// ancestor directory down to the file's parent. Relative/empty paths are
// ignored. Missing paths are fine — Landlock grants are installed with
// ignore-if-missing semantics, so a configured browser/tool path can be wired
// before the binary exists.
func (p Policy) WithExecPath(path string) Policy {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || !filepath.IsAbs(path) {
		return p
	}
	p = grantExecPath(p, path)
	if interp, ok := wslInteropInterpreterFor(path); ok {
		p = grantExecPath(p, interp)
	}
	return p
}

func grantExecPath(p Policy, path string) Policy {
	p.ReadFiles = appendUniquePath(p.ReadFiles, path)
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator) && dir != ""; dir = filepath.Dir(dir) {
		p.ReadDirs = appendUniquePath(p.ReadDirs, dir)
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
	}
	return p
}

func wslInteropInterpreterFor(path string) (string, bool) {
	if !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return "", false
	}
	b, err := os.ReadFile("/proc/sys/fs/binfmt_misc/WSLInterop")
	if err != nil {
		return "", false
	}
	var enabled bool
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "enabled" {
			enabled = true
			continue
		}
		if rest, ok := strings.CutPrefix(line, "interpreter "); ok && enabled {
			interp := filepath.Clean(strings.TrimSpace(rest))
			if filepath.IsAbs(interp) {
				return interp, true
			}
			return "", false
		}
	}
	return "", false
}

// EnvOverride reports whether ZARLCODE_SANDBOX was set explicitly and, if so,
// which state it requests. The historic contract is preserved: explicit
// off/0/false/no disables; any other explicit value enables.
func EnvOverride() (bool, bool) {
	v, ok := os.LookupEnv(envSwitch)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no":
		return false, true
	default:
		return true, true
	}
}

// EnabledFromEnvDefault reports whether the ZARLCODE_SANDBOX switch leaves the
// sandbox on, falling back to def when unset. Explicit off/0/false disables;
// any other explicit value enables.
func EnabledFromEnvDefault(def bool) bool {
	if enabled, ok := EnvOverride(); ok {
		return enabled
	}
	return def
}

// EnabledFromEnv reports whether the ZARLCODE_SANDBOX switch leaves the
// sandbox on. Unset means on; only an explicit off/0/false disables.
func EnabledFromEnv() bool {
	return EnabledFromEnvDefault(true)
}

// gitCommonDir resolves the main repository's .git directory when root
// is a linked worktree (its .git is a one-line pointer file). Returns
// "" for regular repositories and non-repos — the workspace grant
// already covers those.
func gitCommonDir(root string) string {
	dotGit := filepath.Join(root, ".git")
	fi, err := os.Lstat(dotGit)
	if err != nil || fi.IsDir() {
		return ""
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitdir == "" {
		return ""
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	// gitdir points at <main>/.git/worktrees/<name>; grant the whole
	// .git so shared objects and refs are reachable. Fall back to the
	// pointer target itself if the layout is unfamiliar.
	for dir := gitdir; ; {
		if filepath.Base(dir) == ".git" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return gitdir
		}
		dir = parent
	}
}

// splitPathList splits a colon-separated path list, dropping empty and
// relative entries — a relative grant would silently depend on the
// parent's cwd, which is never what the operator meant.
func splitPathList(s string) []string {
	if s == "" {
		return nil
	}
	var paths []string
	for p := range strings.SplitSeq(s, string(os.PathListSeparator)) {
		if p != "" && filepath.IsAbs(p) {
			paths = append(paths, p)
		}
	}
	return paths
}

func appendUniquePath(paths []string, p string) []string {
	p = filepath.Clean(p)
	if p == "" {
		return paths
	}
	if slices.Contains(paths, p) {
		return paths
	}
	return append(paths, p)
}
