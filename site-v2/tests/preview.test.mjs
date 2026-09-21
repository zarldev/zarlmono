import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';
import AxeBuilder from '@axe-core/playwright';

// Run against `npm run preview` after building; dev mode has no Pagefind index.
const base = process.env.V2_URL || 'http://127.0.0.1:4322/zarlmono/';
const screenshots = new URL('../.playwright/', import.meta.url);

test('V2 responsive pages, accessibility, links, recording, clipboard and docs search', { timeout: 180_000 }, async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'], reducedMotion: 'reduce' });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await mkdir(screenshots, { recursive: true });
    const links = new Set();
    for (const route of ['', 'toolkit/', 'zarlcode-onboarding/', 'getting-started/', 'architecture/']) {
      for (const width of [1280, 768, 390, 320]) {
        await page.setViewportSize({ width, height: 844 });
        const response = await page.goto(new URL(route, base).href);
        assert.equal(response.status(), 200, route);
        await page.evaluate(() => document.fonts.ready);
        // Expressive Code assigns focusability after its deferred layout measurement.
        await page.waitForFunction(() => [...document.querySelectorAll('.expressive-code pre')].every((pre) => pre.scrollWidth <= pre.clientWidth || pre.tabIndex === 0));
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${route || 'home'} overflows at ${width}px`);
        await page.screenshot({ path: new URL(`${route.replaceAll('/', '') || 'home'}-${width}.png`, screenshots).pathname });
        if (width === 390 && !route) {
          for (const selector of ['.architecture-map', '.workbench', '.workflow-grid', '.toolkit-panel', '.install-card', '.site-footer']) {
            await page.locator(selector).scrollIntoViewIfNeeded();
            await page.screenshot({ path: new URL(`home-${selector.slice(1)}-390.png`, screenshots).pathname });
          }
        }
        if (width === 390 && route === 'toolkit/') {
          for (const selector of ['.runtime-flow', '.extension-list', '.code-window']) {
            await page.locator(selector).scrollIntoViewIfNeeded();
            await page.screenshot({ path: new URL(`toolkit-${selector.slice(1)}-390.png`, screenshots).pathname });
          }
        }
        if (width === 1280 || width === 390) {
          const accessibility = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
          // Shared V1 architecture markup has an image-role wrapper around links and
          // a non-focusable scrolling table. Record those inherited findings only;
          // this presentation prototype deliberately does not modify V1 content.
          const inherited = route === 'architecture/' ? { 'nested-interactive': '.arch-stack', 'scrollable-region-focusable': 'table' } : {};
          const violations = accessibility.violations.filter((violation) => !violation.nodes.every((node) => node.target.join() === inherited[violation.id]));
          assert.deepEqual(violations.map(({ id, nodes }) => ({ id, elements: nodes.map((n) => n.target) })), [], `accessibility: ${route} ${width}`);
        }
      }
      if (route === 'getting-started/') {
        const snippet = await page.locator('.expressive-code').allTextContents();
        assert.ok(snippet.some((text) => text.includes('tools.New(') && text.includes('tools.SchemaFor[weatherArgs]')));
        assert.ok(snippet.every((text) => !text.includes('DecodeArgs') && !text.includes('weather{}')));
      }
      if (route === '' || route === 'toolkit/') {
        assert.equal(await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Architecture', exact: true }).count(), 1);
        const explanation = route === '' ? '.architecture-map .layer-list > li' : '.runtime-flow > li';
        assert.equal(await page.locator(explanation).count(), 4, 'architecture is explained on the page');
        if (route === 'toolkit/') {
          assert.equal(await page.locator('.extension-list dt').count(), 5);
          assert.match(await page.locator('.code-window').textContent(), /newWeatherTool\(\)/);
        }
        for (const href of await page.locator('a[href]').evaluateAll((nodes) => nodes.map((node) => node.href))) links.add(href);
        for (const button of await page.locator('[data-copy]').all()) {
          await button.click();
          await page.waitForFunction((command) => [...document.querySelectorAll('[data-copy]')].find((el) => el.dataset.copy === command)?.textContent === 'Copied ✓', await button.getAttribute('data-copy'));
          assert.equal(await context.pages()[0].evaluate(() => navigator.clipboard.readText()), await button.getAttribute('data-copy'));
          assert.equal(await button.textContent(), 'Copied ✓');
        }
      }
    }
    for (const href of links) {
      const url = new URL(href);
      if (url.origin !== new URL(base).origin) continue;
      assert.equal((await page.request.get(href)).status(), 200, href);
      await page.goto(href);
      if (url.hash) assert.ok(await page.evaluate((id) => !!document.getElementById(id), decodeURIComponent(url.hash.slice(1))), `missing anchor: ${href}`);
    }
    console.log(`Checked ${[...links].filter((href) => href.startsWith(base)).length} distinct local links/anchors.`);
    await page.goto(base);
    await page.keyboard.press('Tab');
    assert.equal(await page.locator(':focus').textContent(), 'Skip to content');
    assert.equal(await page.locator(':focus').evaluate((el) => getComputedStyle(el).outlineStyle), 'solid');
    await page.keyboard.press('Enter');
    assert.equal(await page.locator(':focus').getAttribute('id'), 'main');
    const image = page.locator('#workbench-recording');
    const poster = await image.getAttribute('src');
    assert.ok(poster.endsWith('.webp'), 'initial recording is a still');
    await page.getByRole('button', { name: 'Play recording' }).click();
    await page.waitForFunction(() => { const image = document.querySelector('#workbench-recording'); return image.src.endsWith('.gif') && image.complete && image.naturalWidth > 0; });
    await page.getByRole('button', { name: 'Stop recording' }).click();
    assert.equal(await image.getAttribute('src'), poster);
    await page.evaluate(() => { Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => { throw new Error('Denied'); } } }); });
    await page.locator('[data-copy]').first().click();
    assert.equal(await page.locator('.copy-status').first().textContent(), 'Clipboard unavailable. Select the command and copy it manually.');

    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 844 });
      await page.goto(new URL('zarlcode-onboarding/', base).href);
      await page.getByRole('button', { name: 'Search', exact: true }).click();
      await page.getByRole('textbox', { name: 'Search', exact: true }).fill('provider');
      await page.locator('.pagefind-ui__result-link').first().waitFor();
      assert.ok(await page.locator('.pagefind-ui__result-link').count() > 0);
      await page.screenshot({ path: new URL(`search-${width}.png`, screenshots).pathname });
      await page.locator('.pagefind-ui__result-link').first().click();
      await page.waitForLoadState();
      assert.ok(page.url().startsWith(base));
      if (width === 390) {
        await page.getByRole('button', { name: 'Menu', exact: true }).click();
        assert.ok(await page.getByRole('link', { name: '← Project overview', exact: true }).isVisible());
      }
      for (const theme of ['light', 'dark']) {
        await page.locator('starlight-theme-select select:visible').selectOption(theme);
        assert.equal(await page.locator('html').getAttribute('data-theme'), theme);
      }
      if (width === 390) await page.getByRole('button', { name: 'Menu', exact: true }).click();
    }
    assert.deepEqual(errors, [], 'browser runtime errors');
  } finally {
    await browser.close();
  }
});
