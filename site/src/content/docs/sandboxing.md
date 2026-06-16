---
title: Sandboxing
description: Two independent layers keep an agent's effects inside the workspace — static shell-command vetting and kernel-enforced Landlock confinement. Defence in depth, and both fail closed.
---

A coding agent runs `bash`. That's the most dangerous tool it has, and
"the model promised not to `rm -rf $HOME`" is not a security boundary.
zkit puts two independent layers in front of it:
`zkit/agent/shellpolicy` reads the command and decides whether it runs
at all; `zkit/agent/sandbox` confines the process that does run so it
physically can't touch anything outside the workspace. The policy is
the cheap first pass; the sandbox is the wall behind it. Neither
trusts the other, and neither trusts the model.

## shellpolicy — static command vetting

`shellpolicy` parses a `bash` command with `mvdan.cc/sh` and lowers it
to a **platform-neutral IR**: a set of normalised risk codes (`cd`,
`redirect`, `subshell`, `expansion`, …), never raw syntax nodes, so
the policy decisions stay stable across parser upgrades.

`Decide` is the baseline ruleset. It blocks four things, and names each
rejection so the model can route around it:

- **`cd`** — the shell's working directory is pinned to the workspace
  root by design; `cd` is a boundary-escape vector, so the rejection
  points at the workspace-bounded tools (`ls`, `grep`, `read`) instead.
- **output redirection to a real file** (`> out.txt`) — there's a
  `write`/`edit` tool that respects the workspace; redirects to
  `/dev/null` are fine.
- **syntax errors** — the command wouldn't run anyway; better a clean
  "this didn't parse" than a half-executed line.
- **IR version mismatch** — fail closed if a cached IR is stale.

### Verify mode is stricter

A `verify` sub-agent ([work modes](/zarlmono/spawn/#work-modes)) may
build and test but must not mutate the workspace. When ctx carries
`WorkMode == verify`, the guardrail switches to `DecideVerify`, which
adds a deny-list of mutating commands (`rm`, `mv`, `cp`, `tee`,
`chmod`, repo-state `git` like `commit`/`checkout`/`reset`,
module-mutating `go mod`/`go get`) plus a **static write-target
analysis** that extracts the file operands a command would create or
destroy — unwrapping `sudo`/`xargs`/`timeout` wrappers and modelling
`git`'s pre-subcommand global flags, so `git -C . rm foo_test.go`
doesn't slip through. It deliberately *allows* `npm`/`pip install`
(tests need deps), `kill` (a verify run may stop its own server), and
plain `sed`/`perl` without `-i` (pure filters that mutate nothing).

### What it is, and isn't

shellpolicy is static analysis, not a sandbox — it can't see through
`eval` or a command assembled inside a string, and it isn't trying to.
Its job is to raise the bar from "any `bash` call can mutate the world"
to "mutation requires deliberate evasion"; the kernel sandbox below is
what catches the evasion. `guardrails.NewShellGuardrail(code.ToolNameBash)`
wires it as a pre-dispatch hook, and a blocked command returns a
`tools.Validation` error carrying the block reason — which the model
sees on its next turn.

## sandbox — kernel-enforced confinement

`zkit/agent/sandbox` uses Linux **Landlock** for the filesystem and
**user + network namespaces** for the network. A `Policy` declares
what's reachable; `DefaultPolicy(workspaceRoot)` is the coding-agent
preset:

| Access | Paths |
|---|---|
| **read-only** | system trees (`/usr`, `/bin`, `/lib*`, `/etc`, `/proc`, …), `~/.config/git`, `~/go/bin`, `~/.local/bin` |
| **read-write** | the workspace root, `/tmp` · `/var/tmp` · `/dev/shm`, the toolchain caches (`~/.cache`, `$GOCACHE`, `$GOMODCACHE`) |
| **denied** | everything else — explicitly `~/.ssh`, `~/.aws`, `~/.zarlcode`, the rest of `$HOME` |

Operators widen it without touching code via `ZK_SANDBOX_RO` /
`ZK_SANDBOX_RW` (colon-separated paths), and `policy.WithExecPath(bin)`
grants a specific binary plus its parent directories — that's how a
configured browser or an askpass helper becomes runnable inside the
confinement.

### The re-exec shim

Landlock restrictions apply to the *calling* process and are inherited
across `exec`. A process can't restrict itself and then keep running
unrestricted — so the confinement happens in a short-lived shim:

1. `sandbox.New(policy)` serialises the policy into a
   `ZK_SANDBOX_POLICY` env var and rewrites your `*exec.Cmd` to run
   `/proc/self/exe` behind a `zk-sandbox-shim` marker arg.
2. Every wired binary calls `sandbox.ExecShim()` as the **first thing
   in `main()`**. On a normal launch it's a no-op; when it sees the
   marker it never returns — it applies the Landlock ruleset, brings up
   loopback if the network is isolated, and `exec`s the real argv in
   place.

That ordering is load-bearing: `ExecShim` has to win before any other
startup code opens a descriptor the policy would forbid.

### The subtleties that make it work

- **`WithRefer()` on write grants.** Without it Landlock denies
  cross-directory rename/link *inside* a granted tree — which breaks
  `git` (tmp → objects) and `go build`. This one flag is the
  difference between a sandbox that works and one that fails every
  build.
- **`V3.BestEffort()` + `IgnoreIfMissing()`.** The ruleset targets
  Landlock ABI v3 but downgrades cleanly on older kernels, and a
  configured-but-absent path is skipped rather than fatal — the same
  policy runs on a current laptop and an older CI box.
- **Network isolation via namespaces.** With `AllowNetwork` false, the
  command clones into a fresh user + network namespace with zero host
  interfaces — no host `127.0.0.1`, no internet — and the shim brings
  up `lo` with a raw ioctl (no netlink dependency). The caller's
  uid/gid map to themselves, so workspace files keep sane ownership
  instead of showing up as `nobody`.

### It fails closed

`sandbox.New` returns an **error** when the kernel can't enforce the
policy — Landlock ABI below 1, or any non-Linux host. It never
silently degrades, because a sandbox that quietly does nothing is worse
than none: it lets a "confined" run lie about its safety. The caller
decides what to do with the error — zarlcode logs a warning and runs
`bash` unconfined (the shell policy still applies); an eval harness
might refuse to start. The choice is explicit, at the call site.
