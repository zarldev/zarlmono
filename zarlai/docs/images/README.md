# Screenshots & recordings

This directory holds visual assets referenced from the project
[README](../../README.md) and the [running guide](../running.md).

## Files the README expects

| Path | Type | What | Notes |
|---|---|---|---|
| `hero.gif` | GIF (animated) | 5–10 s loop of the Immersive view mid-conversation: voice in → response → TTS → ambient panel update. Hero of the README — spend the most effort here. | ≤ 1280×720, ≤ 5 MB target |
| `immersive.png` | PNG | The default `/` view: camera tile, talking head, any visible floating panels (now-playing, thinking) | 1920×1080 native, can crop |
| `onboard.png` | PNG | A representative step of the `/onboard` wizard (the voice-picker step shows the most personality) | Avoid the address step — placeholder text |
| `identity.png` | PNG | `/admin → Identity` — agent-tuner surface (display name, voice, avatar swatches, gestures, model) | Avatar centred in frame; sidebar visible for admin context |
| `admin-tools.png` | PNG | `/admin` → Tools view (tool providers + per-tool toggles) | Showcase the dynamic-tool-selection ranking if you can |

## Capture tips

- **Restart with the latest build first.** If you build new code but
  the running binary is older, screenshots show stale UI.
- **Browser zoom = 100%** for consistency.
- **Window chrome:** crop the browser bar out unless it adds context.
- **Dark / light:** the UI is dark by default — keep it dark across
  the gallery for visual coherence.
- **No personal data on screen:** real names, faces, messages, HA
  state with addresses, etc. Use a test persona.

### Tools on Windows

zarl runs in WSL but the browser is on Windows, so capture must be
Windows-native (you can't grab the Windows desktop from inside WSL).

- **Screenshots (PNG):**
  - `Win + Shift + S` (built-in Snipping Tool) — region select → clipboard
    or saved file. Simplest path.
  - [ShareX](https://getsharex.com/) — free, one-stop tool with
    region capture, custom save paths, optional auto-upload.
- **Screen recordings → GIF:**
  - ShareX is the easiest end-to-end: *Capture → Screen recording (GIF)*
    → save as `.gif` directly. Set FPS to 15, output dimensions to 1280
    wide, region to the browser viewport.
  - Xbox Game Bar (`Win + G`) records to MP4 but is application-bound
    (won't capture arbitrary regions). Useful as a fallback.
- **Optimising the GIF after capture (optional but recommended):**
  - Run `ffmpeg` from a Windows terminal (install via
    `winget install Gyan.FFmpeg` or Chocolatey):
    ```
    ffmpeg -i in.mp4 -vf "fps=15,scale=1280:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" -loop 0 hero.gif
    ```
  - Or use [gifski](https://gif.ski/) (`winget install ImageOptim.Gifski`)
    for smaller, sharper GIFs from MP4.

### Getting captures into the repo

Windows-side files live under `C:\Users\<you>\…`; the image directory in this
repo is `zarlai/docs/images/`. If the checkout is in WSL, two easy options are:

- **From the WSL terminal** — Windows drives mount under `/mnt/c/`:
  ```bash
  cp /mnt/c/Users/<you>/Pictures/hero.gif docs/images/hero.gif
  ```
- **From Windows Explorer** — type the checkout path under
  `\\wsl$\<distro>\...\zarlai\docs\images\`
  in the address bar (replace `<distro>` with e.g. `Ubuntu`), then
  drag the captured file in.

Aim for 10–15 fps, 5–10 s loops, under 5 MB so GitHub renders the GIF
inline rather than as a download link.

## Adding new shots

1. Drop the file at the path above (keep the filenames stable so the
   README links don't rot).
2. If you add new shots beyond the README's table, reference them
   from a relevant doc rather than orphaning them here.
3. Run `git add docs/images/<file>` — the parent `data/` was
   gitignored to exclude bulk fixtures, but `docs/images/` is
   tracked.

## Optimisation (Windows)

Before committing, shrink large captures:

- **PNG:** [TinyPNG](https://tinypng.com/) drag-and-drop, or
  `pngquant` (`winget install pngquant`):
  ```
  pngquant --quality=80-95 --skip-if-larger --output out.png in.png
  ```
- **GIF:** ShareX has a built-in re-encoder, or use `gifsicle`
  (`scoop install gifsicle`):
  ```
  gifsicle -O3 --lossy=80 in.gif > out.gif
  ```

Aim for combined `docs/images/` under 10 MB.
