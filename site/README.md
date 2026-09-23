# Project website

`site/` is the single Astro + Starlight website deployed to
https://zarldev.github.io/zarlmono/ by `.github/workflows/docs.yml`.

- `src/pages/`: product homepage and zkit toolkit landing page.
- `src/content/docs/`: canonical guides and reference; existing documentation URLs are retained.
- `public/`: canonical CLI recordings, byte-identical to `zarlcode/docs/images/`.
- `src/assets/`: optimized still-first recording poster; see its README for regeneration.

## Verify

From the repository root:

```sh
go tool task docs:check
```

This checks source links and quickstart/media parity, compiles the maintained Go
quickstart, installs locked npm dependencies, builds all pages and the search
index, runs Chromium browser checks, and audits dependencies. CI installs the
required browser system libraries; local machines need Playwright's Chromium
prerequisites (`npm --prefix site run test:install:ci` installs them on supported
Linux distributions and may require sudo).

Browser tests use Playwright's managed preview server on port 4322. The runner
starts and stops it; no separate preview process is required. Tests have bounded
timeouts and do not silently attach to an existing server.

Coverage includes every generated page's local links/anchors/media, 404 behavior,
320/390/768/1280px layouts, all four palettes, light/dark docs themes, keyboard
navigation, clipboard success/failure, recording controls, search, unavailable
storage, runtime errors, and targeted axe WCAG A/AA checks with no exclusions.
Screenshots go to ignored `.playwright/`; failures go to `test-results/`.
Automated checks are not cross-browser or full accessibility certification.

## Edit and preview

```sh
npm --prefix site run dev
# Or test the production build and Pagefind search:
npm --prefix site run build
npm --prefix site run preview
```

The production preview is http://127.0.0.1:4322/zarlmono/; stop it with Ctrl+C
before running browser tests. Keep the `/zarlmono/` base on local links.
The toolkit excerpt is extracted from `examples/quickstart/main.go`; if the
excerpt selector fails, update the selector rather than creating a second,
drifting example. Recording playback remains opt-in and never autoplays.
