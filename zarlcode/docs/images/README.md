# zarlcode screenshots and recordings

This directory owns the canonical Charm VHS tapes and rendered GIFs for the zarlcode
site. Astro serves synchronized copies from `site/public/`; do not edit those copies
by hand.

## Deterministic recording setup

Every tape sources `recording-setup.sh`, which builds a temporary zarlcode binary,
creates an isolated workspace and `HOME`/XDG state tree, and starts
`recording-fixture/`: a local, deterministic OpenAI-compatible server on
`127.0.0.1:8081`. The fixture writes its bound address to the tape-owned temporary
directory; setup verifies that the owned process is alive before exporting
`LLAMACPP_BASE_URL`. A shell trap handles normal cleanup, and the fixture also watches
its parent shell so an interrupted VHS run cannot leave the listener behind.

The fixture is a standalone nested Go module. It never contacts a provider, GitHub, or
the public network, and all recordings use synthetic values only. In particular, the
provider/vault tape uses `smoke-secret` and `smoke-passphrase`; never record a real API
key, OAuth token, or passphrase.

## Prerequisites

Render from the repository root with these local tools installed:

- the root Go tool directive for [`vhs`](https://github.com/charmbracelet/vhs), invoked only as `go tool vhs`
- `ffmpeg`
- `ttyd`
- Go and `curl`

The fixture can be checked independently without joining the root workspace:

```bash
GOWORK=off go -C zarlcode/docs/images/recording-fixture test ./...
```

## Render the canonical GIFs

```bash
go tool vhs zarlcode/docs/images/hero.tape
go tool vhs zarlcode/docs/images/onboarding-local.tape
go tool vhs zarlcode/docs/images/onboarding-provider.tape
go tool vhs zarlcode/docs/images/workflow-demo.tape
go tool vhs zarlcode/docs/images/screen-cockpit.tape
go tool vhs zarlcode/docs/images/screen-fileviewer.tape
go tool vhs zarlcode/docs/images/screen-modelpicker.tape
go tool vhs zarlcode/docs/images/screen-planmode.tape
go tool vhs zarlcode/docs/images/screen-subagents.tape
go tool vhs zarlcode/docs/images/screen-workingset.tape
```

Render sequentially: every tape owns the same local fixture port. Each command
writes its GIF and named PNG screenshots next to its tape. All clips use a
1440×900 terminal, 18 px type, and 15 fps. Setup and cleanup remain hidden; only the
two onboarding clips show first-run setup. Keep generated media under version
control after reviewing it; do not use an ambient `zarlcode`, provider, or workspace.

Posters use VHS `Screenshot` at a named stable state, not timestamp extraction.
Keep a short `Sleep` after `Screenshot`: VHS captures the **next** visible frame,
so an immediate `Hide` can drop the screenshot and a keypress can change its state.

## Synchronize and verify public assets

After rendering, copy the canonical GIFs into Astro's public directory, then verify the
copies are byte-for-byte identical:

```bash
go run ./tools/docmedia/cmd/docmedia -sync
go run ./tools/docmedia/cmd/docmedia
```

`go test -count=1 ./tools/doccheck/...` also runs the no-write synchronization check.

| Canonical GIF | Astro public GIF |
| --- | --- |
| `hero.gif` | `site/public/zarlcode-hero2.gif` |
| `onboarding-local.gif` | `site/public/zarlcode-onboarding-local.gif` |
| `onboarding-provider.gif` | `site/public/zarlcode-onboarding-provider.gif` |
| `workflow-demo.gif` | `site/public/zarlcode-workflow-demo.gif` |
| `screen-cockpit.gif` | `site/public/zarlcode-cockpit.gif` |
| `screen-fileviewer.gif` | `site/public/zarlcode-fileviewer.gif` |
| `screen-modelpicker.gif` | `site/public/zarlcode-modelpicker.gif` |
| `screen-planmode.gif` | `site/public/zarlcode-planmode.gif` |
| `screen-subagents.gif` | `site/public/zarlcode-subagents.gif` |
| `screen-workingset.gif` | `site/public/zarlcode-workingset.gif` |

## Clip coverage

| Tape | Purpose and poster state |
| --- | --- |
| `hero.tape` | Plan → real edit/test → populated **Ready to review** summary → inspectable one-file diff. `hero-poster.png` is the homepage still; `hero-diff.png` records the diff. |
| `onboarding-local.tape` | Accept local defaults and reach Build mode without a cloud account. |
| `onboarding-provider.tape` | Save a provider key in the vault with synthetic credentials; end on the global-save confirmation. |
| `workflow-demo.tape` | Plan without edits, Build and test, inspect the diff, then resume the saved conversation. `workflow-demo-resume.png` captures the restored review state. |
| `screen-cockpit.tape` | Inspect context and session information after establishing what the greeting package does. |
| `screen-fileviewer.tape` | Inspect `greet.go`, then `greet_test.go`; end with the behavior test previewed and both files listed. |
| `screen-modelpicker.tape` | Inspect the discovered local fixture model from a populated conversation. |
| `screen-planmode.tape` | Plan a behavior-preserving refactor with **No files changed**, then hand off to Build. |
| `screen-subagents.tape` | Delegate a greeting-refactor review and inspect the delegated task panel. |
| `screen-workingset.tape` | Make and test the edit, inspect its diff, then roll back `greet.go`. `screen-workingset-rollback.png` captures the rollback result. |

Every tape writes `<tape-name>-poster.png`. The hero, workflow, and working-set
clips additionally write the named secondary screenshots above. The workspace is
always the copied `greeting-demo` fixture, never the repository being developed.
