---
title: Providers and credentials
description: Configure providers in the zarlcode TUI and understand local credential storage, vault protection, and settings scope.
---

Provider setup belongs in the TUI for normal use. Start zarlcode in the target workspace,
then press **`Ctrl+S`** and select **Providers**. From there you can add or select an API
key provider, sign in with supported OAuth flows, or configure a custom OpenAI-compatible
endpoint. Use **`Ctrl+E`** to select a provider/model pair during a session.

## Providers and models

zarlcode supports hosted providers including Anthropic, OpenAI, DeepSeek, Gemini, and
Google Vertex, plus local and OpenAI-compatible endpoints such as Ollama and llama.cpp.
It configures those endpoints but does not run the local server for you.

The Providers panel can fetch and select available models for the active provider. Model
selection from the picker is workspace-scoped, so one repository can use a different
model than another without changing every workspace.

## Credential vault

Credentials live locally in `~/.zarlcode/state.db`. Passphrase encryption is the default:
the **first secret you save** creates and protects the vault. Fresh local-only startup
does not create an unused vault.

Later interactive launches ask you to unlock stored credentials before they are used.
You can leave the unlock screen with `Esc` or `Ctrl+C`; that exits rather than continuing
with locked credentials. Headless operation never prompts, so a headless task that needs
a locked stored credential fails closed.

There is no passphrase recovery and no environment-variable passphrase fallback. Back up
`state.db` together with `master.kdf`, and keep the passphrase separately. Older
random-key `master.key` credential data is unsupported: re-enter those credentials so
they can be stored with passphrase protection. zarlcode does not convert or delete those
rows automatically.

Plaintext credential storage is an explicit advanced opt-out, not a first-run choice.
Use the automation reference only when you understand that trade-off.

## Global defaults and workspace settings

The first-run wizard saves its provider/model defaults globally. Settings changed later
from the settings pane are normally scoped to the current workspace, which lets a project
override the default. Focus a workspace setting and press **`p`** to promote it to the
global default; promotion moves the value instead of maintaining two divergent copies.

Provider keys and completed OAuth credentials are global. The settings panel also exposes
the effective scope so you can see whether a value comes from this workspace or from the
global default.

## Command-line management

`zarlcode keys` remains available for non-TUI setup and operations. It can list masked
keys, set a provider key, begin supported OAuth flows, and report or change vault
protection. It is an advanced interface: the normal path is still `zarlcode` followed by
`Ctrl+S`.

See [Automation and CLI](/zarlmono/zarlcode-automation/) for command-line examples and
headless limitations. See the [interface guide](/zarlmono/zarlcode-interface/) for the
provider and model controls in context.
