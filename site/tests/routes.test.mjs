import assert from 'node:assert/strict';
import { readdir } from 'node:fs/promises';
import { test } from '@playwright/test';

const base = 'http://127.0.0.1:4322/zarlmono/';

// Check every generated route, not just pages linked from the marketing layout.
test('all generated pages retain working local links, anchors and media', async ({ page, request }) => {
  const files = await readdir(new URL('../dist/', import.meta.url), { recursive: true });
  const routes = files.filter((file) => file.endsWith('.html'));
  assert.ok(routes.length > 25, 'canonical documentation must survive promotion');
  const pages = new Map();
  const resources = new Set();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const file of routes) {
    const route = file === 'index.html' ? '' : file.replace(/index\.html$/, '');
    const url = new URL(route, base);
    assert.equal((await page.goto(url.href)).status(), 200, url.href);
    const document = await page.evaluate(() => ({
      ids: [...document.querySelectorAll('[id]')].map((node) => node.id),
      links: [...document.querySelectorAll('a[href]')].map((node) => node.href),
      media: [...document.querySelectorAll('img[src], video[src], source[src], script[src], link[rel="stylesheet"]')]
        .map((node) => node.src || node.href),
    }));
    pages.set(url.pathname, document);
    for (const href of [...document.links, ...document.media]) {
      const link = new URL(href);
      if (link.origin === url.origin) resources.add(link.href);
    }
  }
  const checked = new Set();
  for (const href of resources) {
    const url = new URL(href);
    assert.ok(url.pathname.startsWith('/zarlmono/'), `link escapes deployment base: ${href}`);
    if (!checked.has(url.pathname)) {
      assert.equal((await request.get(href)).status(), 200, href);
      checked.add(url.pathname);
    }
    if (url.hash) {
      const target = pages.get(url.pathname);
      assert.ok(target, `anchor target is not a generated page: ${href}`);
      assert.ok(target.ids.includes(decodeURIComponent(url.hash.slice(1))), `missing anchor: ${href}`);
    }
  }
  assert.equal((await request.get(new URL('missing-documentation-route/', base).href)).status(), 404);
  assert.deepEqual(errors, [], 'browser runtime errors across generated pages');
  console.log(`Checked ${routes.length} generated pages and ${resources.size} local links/media/anchors.`);
});
