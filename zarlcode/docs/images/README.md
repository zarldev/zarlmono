# zarlcode screenshots & recordings

This directory holds visual assets and VHS source tapes for `zarlcode`.

## Files

| Path | Type | Purpose |
|---|---|---|
| `hero.tape` | VHS tape | Main terminal demo: launch the TUI and run two short `web_fetch` prompts. |
| `screen-cockpit.tape` | VHS tape | Expanded context dashboard (`Ctrl+L`): token/cost/throughput sparklines. |
| `screen-fileviewer.tape` | VHS tape | File viewer (`Ctrl+F`): browse files, skills, agents, hooks. |
| `screen-modelpicker.tape` | VHS tape | Model quick picker (`Ctrl+E`): provider tabs and model list. |
| `screen-planmode.tape` | VHS tape | Plan ↔ Build mode toggle (`Shift+Tab`). |
| `screen-subagents.tape` | VHS tape | Parallel sub-agent runs in the timeline. |
| `screen-workingset.tape` | VHS tape | Working set (`Ctrl+W`): diff and rollback touched files. |
| `hero.gif` | GIF | Rendered output from `hero.tape`. |
| `screen-cockpit.gif` | GIF | Rendered output from `screen-cockpit.tape`. |
| `screen-fileviewer.gif` | GIF | Rendered output from `screen-fileviewer.tape`. |
| `screen-modelpicker.gif` | GIF | Rendered output from `screen-modelpicker.tape`. |
| `screen-planmode.gif` | GIF | Rendered output from `screen-planmode.tape`. |
| `screen-subagents.gif` | GIF | Rendered output from `screen-subagents.tape`. |
| `screen-workingset.gif` | GIF | Rendered output from `screen-workingset.tape`. |

## Prerequisites

VHS needs a few things installed locally:

- `vhs`
- `ffmpeg`
- `ttyd`

See the Charm VHS README for install options.

## Run from the repo root

Run VHS from the monorepo root so the `zarlcode` command inside the tapes resolves correctly.

```bash
vhs zarlcode/docs/images/hero.tape
vhs zarlcode/docs/images/screen-cockpit.tape
vhs zarlcode/docs/images/screen-fileviewer.tape
vhs zarlcode/docs/images/screen-modelpicker.tape
vhs zarlcode/docs/images/screen-planmode.tape
vhs zarlcode/docs/images/screen-subagents.tape
vhs zarlcode/docs/images/screen-workingset.tape
```

That will write GIFs next to the tapes:

- `zarlcode/docs/images/hero.gif`
- `zarlcode/docs/images/screen-cockpit.gif`
- `zarlcode/docs/images/screen-fileviewer.gif`
- `zarlcode/docs/images/screen-modelpicker.gif`
- `zarlcode/docs/images/screen-planmode.gif`
- `zarlcode/docs/images/screen-subagents.gif`
- `zarlcode/docs/images/screen-workingset.gif`

## Notes

- `hero.tape` and `screen-cockpit.tape` use `web_fetch` prompts so they work even in an empty directory.
- `screen-fileviewer.tape`, `screen-subagents.tape`, and `screen-workingset.tape` ask zarlcode to shallow-clone `gohugoio/hugo` into `demo-repo` so the recordings have real files to browse, map, and edit.
- `screen-workingset.tape` records a real file edit; run it in a throwaway workspace (it will create and modify `demo-repo`).
- If your model/backend is slow, increase the `Sleep` durations in the tape.
- For a polished recording, prefer a warmed-up backend and a stable local model/provider.
## Common workflow

```bash
# from repo root
vhs zarlcode/docs/images/hero.tape

# inspect the gifs
ls -lh zarlcode/docs/images/*.gif
```
