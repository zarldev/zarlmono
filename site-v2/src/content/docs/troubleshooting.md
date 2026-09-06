---
title: Troubleshooting
description: Diagnose zarlcode setup, provider access, locked credentials, blocked tools, session recovery, and headless runs without discarding local state.
---

Start with the smallest check that matches the symptom. Preserve the workspace and local state before trying repairs; deleting the state database is not a general reset procedure.

## Check local readiness

Run these commands from a normal terminal:

```sh
zarlcode doctor
zarlcode --help
```

`doctor` is an **offline, read-only** check of the binary, home layout, state database path, and credential-vault presence. It does not contact a model provider, unlock credentials, open or migrate the database, or check for updates. A clean result therefore does not prove that inference is working.

Missing first-run state is setup guidance. Invalid existing paths produce a non-zero status. For a new installation, follow [Install and first run](/zarlmono/zarlcode-onboarding/); for existing state, investigate the reported path rather than deleting it.

## A local model does not connect

1. Confirm that your model server is running separately. zarlcode configures local endpoints; it does not start Ollama or llama.cpp for you.
2. Open **`Ctrl+S` → Providers** in the affected workspace and check the selected provider and endpoint against your server's configuration.
3. Fetch/select an available model in the Providers panel, then use **`Ctrl+E`** to confirm the active provider/model pair.
4. Check the scope of the setting. A workspace override can differ from the global default even when another repository works.

See [Providers and credentials](/zarlmono/zarlcode-providers/) for the setup and scope rules. Local session storage does not make a hosted model offline: a hosted provider still receives model requests.

## A hosted provider rejects a request

Read the provider error in the timeline before changing configuration. Check that the intended provider/model pair is active and that the selected model is available to your provider account. Configure the appropriate API key or supported OAuth sign-in through **`Ctrl+S` → Providers**.

Credentials are global; model choices can be workspace-scoped. Avoid replacing a global credential just because one workspace has a different model selected. Never include an API key, bearer token, or vault passphrase in a prompt or issue report.

## Stored credentials are locked

Interactive launches ask you to unlock protected credentials before use. Leaving the unlock screen with `Esc` or `Ctrl+C` exits rather than continuing with a locked vault.

There is **no passphrase recovery or environment-variable passphrase fallback**. Keep backups of `state.db` and `master.kdf` together, and keep the passphrase separately. Do not delete either file to bypass an unlock error, and do not disable protection as a troubleshooting shortcut.

Older `master.key` credential data is unsupported; the application does not silently convert it. See the [credential vault guide](/zarlmono/zarlcode-providers/#credential-vault) for the supported setup and migration limitations.

## The agent plans but does not edit

Check the current mode. **Plan mode is read-only**: it is for investigation and a proposal, not file edits or shell execution. Inspect the proposal with **`Ctrl+P`**. If you want it executed, switch to Build with **`Shift+Tab`** and give the task a clear scope.

If a Build-mode tool is refused, read the reported policy reason. Narrow the requested operation or use an allowed workflow rather than trying to evade the guardrail. Build mode still runs with your user privileges; it is not blanket permission to modify unrelated files or external systems.

See [Your first workflow](/zarlmono/zarlcode-workflow/) and [Safety and workspace access](/zarlmono/zarlcode-safety/).

## A command or delegated task appears stuck

Inspect the timeline for the last tool result before submitting another copy of the task.

- Open **`Ctrl+W`** and use `Tab` to reach **Processes**. Inspect the tracked command and its output; stop it through the process controls if it should not continue.
- For delegated work, inspect the [sub-agent task panel](/zarlmono/zarlcode-interface/#sub-agents) to distinguish a running task from a completed task awaiting review.
- Before retrying, inspect the working-set diff. A command can have changed files before it stopped producing output.

Do not assume that silence means no work occurred. The [working-set guide](/zarlmono/zarlcode-interface/#the-working-set) describes file, turn, and process inspection.

## A session does not resume

Run `zarlcode --continue` from the original workspace to select its latest session, or choose a listed session from the intro screen. If the wrong session is selected, confirm the workspace before starting a new task.

Interrupted work is marked as interrupted on resume; that is distinct from a corrupt transcript. A missing or corrupt canonical transcript is rejected rather than reconstructed from compacted model context. Preserve the database and the exact error instead of editing stored rows or deleting the session as a first response.

Local persistence is not a remote backup. Abrupt process or machine failure can lose the newest unflushed streaming text. See [Sessions and transcripts](/zarlmono/sessions-transcripts/) for durability, recovery, and export behavior.

## A headless run exits without asking for credentials

That is intentional. Headless mode never enters first-run setup, asks for a passphrase, or waits for a credential-unlock prompt. It fails when a required stored credential is locked.

Configure a usable provider and authentication before starting automation. Pass either `--prompt-text` or `--prompt-file`, not both, and check the iteration limit for the task. `zarlcode init` creates local setup files but does not configure a usable provider on its own.

Read [Automation and CLI](/zarlmono/zarlcode-automation/) for the supported flags and non-interactive behavior. Do not weaken credential protection just to make a job pass.

## Report a reproducible problem

Include the command or TUI action, operating system, installed release, provider/model name, exact error, and the smallest reproduction you can share. Say whether it occurs in one workspace or several and whether it is interactive or headless.

Review diagnostic output and any exported transcript before sharing it: they can contain workspace paths, source code, prompts, or tool output. Remove private content and secrets. Do not upload the state database or credential files. File the sanitized report in the [project issue tracker](https://github.com/zarldev/zarlmono/issues).
