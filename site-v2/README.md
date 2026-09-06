# Website V2 preview

An isolated Astro + Starlight design experiment, **not the live site**. No deployment workflow points here, and adoption has not been decided.

- `/zarlmono/`: product-first zarlcode homepage, Plan → Build → Inspect workflow, install commands, and an opt-in recording.
- `/zarlmono/toolkit/`: developer landing page for zkit, with a wiring excerpt extracted from the maintained quickstart.
- Documentation stays in Starlight, with shared content loaded from `../site/src/content/docs/` and public media from `../site/public/`. Keep both directories together; this is not a standalone content copy.

## Preview

From the repository root:

```sh
npm --prefix site-v2 ci
npm --prefix site-v2 run build
npm --prefix site-v2 run preview
```

Open **http://127.0.0.1:4322/zarlmono/**. Stop the preview with Ctrl+C.

For editing with hot reload, use `npm --prefix site-v2 run dev`. Use the built preview for search testing: Pagefind generates its index during the build.

## Browser verification

With the built preview running, in a second terminal:

```sh
cd site-v2
npx playwright install chromium
npm test
```

The test closes its browser in a `finally` block; the preview remains owned by the first terminal. Set `V2_URL` to test another local preview address, including its trailing `/zarlmono/` prefix.

Coverage:

- Homepage, toolkit, onboarding, quickstart, and architecture at 1280, 768, 390, and 320px; no horizontal document overflow.
- All 24 distinct local marketing links/anchors.
- All three marketing command-copy buttons, actual clipboard contents, success feedback, and clipboard-denied feedback.
- Still-first recording, explicit Play/Stop, and reduced-motion browser settings.
- Keyboard skip-link focus and visible focus outline.
- Starlight search results and result navigation on desktop and mobile, mobile navigation menu, and light/dark theme switching.
- Browser runtime errors and targeted axe WCAG A/AA checks at desktop/mobile widths.

Screenshots are written to ignored `.playwright/`, outside the build output. Automated geometry checks pass. A mobile capture was also requested through `computer_observe`, but its response was encoded image data rather than an inspectable image in the agent tool output. **Human visual approval of the desktop/mobile screenshots remains pending.** This is not a cross-browser or full accessibility certification.

The primary GitHub repository, releases, license, and zkit source URLs were checked separately and returned HTTP 200.

## Known limitations

- Starlight emits a non-fatal `Entry docs → 404 was not found` message while generating its fallback page. The build succeeds with 28 pages and a Pagefind index.
- The shared architecture document has two inherited accessibility findings: an image-role wrapper around links, and a horizontally scrolling table without keyboard focus. The test tolerates only those exact findings on that route. Shared V1 content was intentionally not edited.
- The toolkit excerpt selector deliberately fails the build if the maintained quickstart changes shape; update the selector rather than creating a drifting second example.
- Playback uses the redesigned canonical hero GIF; it is not an optimized video. The initial still is captured explicitly after the refactor/test review, never from an arbitrary timestamp. See `src/assets/README.md`.

Prototype source stays under `site-v2/`. The recording refresh lives in
`zarlcode/docs/images/`, with byte-identical GIF copies synchronized into
`site/public/`. Existing `site/` source, deployment configuration, and unrelated
worktree changes are untouched; there has been no deployment or switchover.
