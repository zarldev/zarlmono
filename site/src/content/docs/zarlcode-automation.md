---
title: Automation and CLI
description: Use zarlcode's command-line, credentials, diagnostics, and headless surfaces for scripts, CI, and recovery workflows.
---

The normal product journey is interactive: run `zarlcode`, configure providers with
`Ctrl+S`, then work in the TUI. This page is for automation, CI, diagnostics, and cases
where a command-line surface is the better fit.

## Resume and launch options

```bash
zarlcode --continue
zarlcode --agent reviewer
zarlcode --env .env.local
```

`--continue` resumes the most recent session for the current workspace. `--agent` starts
with a named agent profile, and `--env` loads an environment file before zarlcode reads
its configuration. The interactive TUI remains the default when no headless flag is used.

## Headless tasks

Use headless mode when a script or CI job needs a one-shot task without an alternate-screen
TUI:

```bash
zarlcode --headless --prompt-text 'Run the focused test suite and report failures.'
zarlcode --headless --prompt-file task.md --max-iter 12
```

`--prompt-text` and `--prompt-file` provide the task and cannot be combined. `--max-iter`
overrides the configured iteration cap (`0` uses the configured default). Headless mode
records lifecycle state locally but **never enters first-run setup, opens a credential-unlock
prompt, or asks for a passphrase**. Configure a usable provider and any required credential
before starting it. A task that requires a locked stored credential fails rather than
waiting for input.

Other headless controls include `--prompt-profile compact|standard`, `--report-file <path>`,
`--pprof <address>`, and `--trace <path>`. Run `zarlcode --help` to see the options that
apply to the installed version.

## Initialize without launching

```bash
zarlcode init
```

`zarlcode init` idempotently materializes the same local home and workspace files as a
normal launch, but does not open the TUI. It is useful for controlled automation; a normal
interactive first run does this work automatically.

## Credentials outside the TUI

The `keys` subcommands are the advanced alternative to the Providers panel:

```bash
zarlcode keys list
zarlcode keys set <provider> <key>
zarlcode keys oauth claude-code
zarlcode keys oauth openai-codex
zarlcode keys protect status
```

Key values are global and `keys list` masks them. The first secret write sets up
passphrase protection by default. `keys protect status`, `keys protect on`, and
`keys protect off` manage the protection mode; `off` is an explicit plaintext-storage
opt-out. Read [Providers and credentials](/zarlmono/zarlcode-providers/) first and keep
backups of `state.db` and `master.kdf` when protection is enabled.

Run `zarlcode keys --help` for the supported providers and protection operations.

## Diagnose and update

```bash
zarlcode doctor
zarlcode upgrade
```

`zarlcode doctor` is an offline, read-only readiness check for the running binary, home
layout, state database path, and credential-vault presence. It does not open or migrate
the database, unlock credentials, contact providers, or check for updates. Missing
first-run state is guidance; invalid existing paths return a non-zero status.

`zarlcode upgrade` updates the installed application from GitHub Releases. Do not use it
as a substitute for reviewing a source checkout's own update process.

## More detail

Run `zarlcode --help` and a subcommand's `--help` for the authoritative flag and exit
status behavior of your installed version. For normal interactive use, return to
[First run and onboarding](/zarlmono/zarlcode-onboarding/) or
[Your first workflow](/zarlmono/zarlcode-workflow/).
