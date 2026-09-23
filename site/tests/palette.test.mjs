import assert from 'node:assert/strict';
import { test } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';
import AxeBuilder from '@axe-core/playwright';

const base = 'http://127.0.0.1:4322/zarlmono/';
const screenshots = new URL('../.playwright/', import.meta.url);
const palettes = ['green', 'amber', 'cyan', 'rust'];
const control = (page) => page.locator('palette-select select:visible');

async function showPreferences(page, route, width) {
  if (route === 'getting-started/' && width < 800) {
    await page.getByRole('button', { name: 'Menu', exact: true }).click();
  }
}

async function checkPalette(page, palette) {
  assert.equal(await page.locator('html').getAttribute('data-palette'), palette);
  assert.equal(await control(page).inputValue(), palette);
  assert.equal(await page.evaluate(() => localStorage.getItem('zarlmono-palette')), palette);
}

async function checkControlContrast(page) {
  await control(page).focus();
  const ratios = await control(page).evaluate((el) => {
    const style = getComputedStyle(el);
    const luminance = (color) => {
      const channels = color.match(/[\d.]+/g).slice(0, 3).map((value) => {
        const channel = Number(value) / 255;
        return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4;
      });
      return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
    };
    const contrast = (a, b) => (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
    const bg = luminance(style.backgroundColor);
    return [contrast(luminance(style.color), bg), contrast(luminance(style.borderTopColor), bg), contrast(luminance(style.outlineColor), bg)];
  });
  assert.ok(ratios[0] >= 4.5, `selector text contrast: ${ratios[0]}`);
  assert.ok(ratios[1] >= 3, `selector border contrast: ${ratios[1]}`);
  assert.ok(ratios[2] >= 3, `selector focus contrast: ${ratios[2]}`);
}

test('colour palettes switch immediately, persist across layouts, and remain accessible', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ reducedMotion: 'reduce' });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await mkdir(screenshots, { recursive: true });
    await page.goto(base);
    assert.equal(await control(page).inputValue(), 'green');
    assert.equal(await page.locator('#hero-title').innerText(), 'A coding agent you can run.\nA harness you can build on.');
    assert.deepEqual(await control(page).locator('option').allTextContents(), ['Green', 'Amber', 'Cyan', 'Rust']);
    // Native select supports keyboard selection and has a visible focus ring.
    await control(page).focus();
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await checkPalette(page, 'amber');
    assert.equal(await control(page).evaluate((el) => getComputedStyle(el).outlineStyle), 'solid');

    const backgrounds = new Set();
    const accents = new Set();
    const docsAccents = { light: new Set(), dark: new Set() };
    for (const palette of palettes) {
      await page.goto(base);
      await control(page).selectOption(palette);
      await checkPalette(page, palette);
      const colors = await page.evaluate(() => {
        const root = getComputedStyle(document.documentElement);
        return { bg: root.backgroundColor, accent: root.getPropertyValue('--accent').trim() };
      });
      backgrounds.add(colors.bg);
      accents.add(colors.accent);
      assert.equal(await page.locator('meta[name="theme-color"]').getAttribute('content'), await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()));
      await page.reload();
      await checkPalette(page, palette);
      await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'zkit' }).click();
      await checkPalette(page, palette);
      await page.getByRole('link', { name: 'Go quickstart ↗', exact: true }).click();
      // Documentation has two copies of the preferences (desktop and mobile).
      assert.deepEqual(await page.locator('palette-select select').evaluateAll((nodes) => nodes.map((node) => node.value)), [palette, palette]);

      for (const route of ['', 'toolkit/', 'getting-started/']) {
        for (const width of [1280, 768, 390, 320]) {
          await page.setViewportSize({ width, height: 844 });
          await page.goto(new URL(route, base).href);
          await showPreferences(page, route, width);
          await checkPalette(page, palette);
          const box = await control(page).boundingBox();
          assert.ok(box.width > 0 && box.x >= 0 && box.x + box.width <= width, `${palette} ${route} control fits at ${width}`);
          assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${palette} ${route} overflows at ${width}`);
          // Exercise the mobile selector and both docs copies, not only its saved value.
          await control(page).selectOption(palettes[(palettes.indexOf(palette) + 1) % palettes.length]);
          await control(page).selectOption(palette);
          await checkPalette(page, palette);
          const themes = route === 'getting-started/' ? ['light', 'dark'] : ['project'];
          for (const theme of themes) {
            if (theme !== 'project') {
              await page.locator('starlight-theme-select select:visible').selectOption(theme);
              assert.equal(await page.locator('html').getAttribute('data-theme'), theme);
              await checkPalette(page, palette);
              docsAccents[theme].add(await page.locator('.site-title').evaluate((el) => getComputedStyle(el).color));
            }
            if (width === 1280 || width === 390) {
              await checkControlContrast(page);
              await page.evaluate(() => document.fonts.ready);
              // Expressive Code makes overflowing blocks focusable after layout.
              await page.waitForFunction(() => [...document.querySelectorAll('.expressive-code pre')].every((pre) => pre.scrollWidth <= pre.clientWidth || pre.tabIndex === 0));
              const accessibility = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
              assert.deepEqual(accessibility.violations.map(({ id, nodes }) => ({ id, elements: nodes.map((node) => node.target) })), [], `${palette} ${route} ${width} ${theme}`);
              await page.screenshot({ path: new URL(`palette-${palette}-${route.replaceAll('/', '') || 'home'}-${width}-${theme}.png`, screenshots).pathname });
            }
          }
        }
      }
      await page.goto(base);
      await checkPalette(page, palette);
    }
    assert.equal(backgrounds.size, 4, 'each project palette changes the background');
    assert.equal(accents.size, 4, 'each project palette changes the accent');
    for (const theme of ['light', 'dark']) assert.equal(docsAccents[theme].size, 4, `each docs palette changes the ${theme} accent`);
    assert.deepEqual(errors, [], 'browser runtime errors');
  } finally {
    await browser.close();
  }
});

test('invalid or unavailable palette storage does not break selection', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const unavailable of [false, true]) {
      const context = await browser.newContext();
      await context.addInitScript((unavailable) => {
        if (unavailable) {
          // Isolate our storage failure from Starlight's independent theme storage.
          for (const method of ['getItem', 'setItem']) {
            const original = Storage.prototype[method];
            Storage.prototype[method] = function (key, ...args) {
              if (key === 'zarlmono-palette') throw new Error('Palette storage disabled');
              return original.call(this, key, ...args);
            };
          }
        } else {
          localStorage.setItem('zarlmono-palette', 'not-a-palette');
        }
      }, unavailable);
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      for (const route of ['', 'getting-started/']) {
        await page.goto(new URL(route, base).href);
        assert.equal(await page.locator('html').getAttribute('data-palette'), 'green');
        await control(page).selectOption('cyan');
        assert.equal(await page.locator('html').getAttribute('data-palette'), 'cyan');
      }
      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally {
    await browser.close();
  }
});
