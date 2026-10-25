import { expect, test, type Page } from "@playwright/test";

async function connect(page: Page, query = "?stream") {
  await page.goto(`/tests/harness.html${query}`);
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Prompt", exact: true }),
  ).toBeVisible();
}
async function push(page: Page, text: string, done = false) {
  await page.evaluate(
    ({ text, done }) => {
      (
        window as unknown as {
          testStream: (text: string, done: boolean) => void;
        }
      ).testStream(text, done);
    },
    { text, done },
  );
}

test("formats unfinished delimiters during real progressive updates and restores exact final syntax", async ({
  page,
}) => {
  await connect(page);
  const message = page.locator(".markdown-message");
  await expect(message.locator("strong")).toHaveText("bo");
  await expect(message).not.toContainText("**");
  await message
    .locator("strong")
    .evaluate((node) => Object.assign(window, { stableStrong: node }));
  await push(page, "This is **bold** and `co");
  await expect(message.locator("code")).toHaveText("co");
  expect(
    await message
      .locator("strong")
      .evaluate(
        (node) =>
          node === (window as unknown as { stableStrong: Node }).stableStrong,
      ),
  ).toBe(true);
  await push(page, "This is **bold** and `code` with [Docs](https://exa");
  await expect(message).toContainText("Docs");
  await expect(message.getByRole("link")).toHaveCount(0);
  await push(
    page,
    "This is **bold** and `code` with [Docs](https://example.com)",
  );
  await expect(message.getByRole("link", { name: "Docs" })).toHaveAttribute(
    "href",
    "https://example.com/",
  );
  // A final unmatched delimiter is literal source, not silently repaired data.
  await push(page, "Final literal **unfinished", true);
  await expect(message).toHaveText("Final literal **unfinished");
  await expect(message.locator("strong")).toHaveCount(0);
  await expect(message).not.toHaveAttribute("data-streaming");
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeVisible();
});

test("interpolates a burst, catches up promptly, and has no idle text mutations", async ({
  page,
}) => {
  await connect(page);
  await push(page, "prefix ");
  const message = page.locator(".markdown-message");
  await expect(message).toHaveText("prefix");
  await message.evaluate((element) => {
    const samples: { length: number; time: number }[] = [];
    const observer = new MutationObserver(() =>
      samples.push({
        length: element.textContent!.length,
        time: performance.now(),
      }),
    );
    observer.observe(element, {
      subtree: true,
      childList: true,
      characterData: true,
    });
    Object.assign(window, { streamSamples: samples, streamObserver: observer });
  });
  const final = "prefix " + "more content ".repeat(30) + "👩🏽‍💻";
  await push(page, final);
  await expect(message).toHaveText(final, { timeout: 2000 });
  const samples = await page.evaluate(
    () =>
      (
        window as unknown as {
          streamSamples: { length: number; time: number }[];
        }
      ).streamSamples,
  );
  expect(
    samples.some((sample) => sample.length > 7 && sample.length < final.length),
  ).toBeTruthy();
  expect(samples.at(-1)!.time - samples[0].time).toBeLessThan(500);
  const count = samples.length;
  await page.waitForTimeout(350);
  expect(
    await page.evaluate(
      () =>
        (window as unknown as { streamSamples: unknown[] }).streamSamples
          .length,
    ),
  ).toBe(count);
  await page.evaluate(() =>
    (
      window as unknown as { streamObserver: MutationObserver }
    ).streamObserver.disconnect(),
  );
});

test("respects reduced motion and flushes cancelled output exactly", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await connect(page);
  await push(page, "Unicode 👩🏽‍💻 and café.");
  const message = page.locator(".markdown-message");
  await expect(message).toHaveText("Unicode 👩🏽‍💻 and café.");
  await push(page, "Final **partial");
  await expect(message.locator("strong")).toHaveText("partial");
  await page.getByRole("button", { name: "■ Stop" }).click();
  await expect(message).toHaveText("Final **partial");
  await expect(message).not.toHaveAttribute("data-streaming");
});

test("renders highlighting, math and scoped footnotes without trusted math commands", async ({
  page,
}) => {
  const external: string[] = [];
  page.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:4173/"))
      external.push(request.url());
  });
  await connect(page, "?features");
  const message = page.locator(".markdown-message");
  await expect(message.locator("del")).toHaveText("Removed");
  await expect(message.locator("strong em")).toHaveText("nested emphasis");
  await expect(message.locator("ul ul li")).toHaveText("Child with code");
  await expect(
    message.locator("code.language-go .hljs-keyword").first(),
  ).toHaveText("func");
  await expect(message.locator("code.language-not-a-language")).toHaveText(
    "keep this literal\n",
  );
  await expect(message.locator(".katex-display .katex")).toHaveCount(1);
  await expect(message.locator(".katex").first()).toBeVisible();
  const reference = message.locator("a[data-footnote-ref]");
  const anchor = await reference.getAttribute("href");
  expect(anchor).toMatch(/^#md-/);
  await reference.click();
  await expect(message.locator(".footnotes")).toContainText(
    "A local reference.",
  );
  await expect(
    message.locator('a[href^="javascript:"], img, iframe, script'),
  ).toHaveCount(0);
  await expect(message.locator('a[target="_blank"]')).toHaveCount(0);
  expect(external).toEqual([]);
});

test("follows rendered growth but does not pull back after scrolling away", async ({
  page,
}) => {
  await connect(page, "?stream&scroll");
  const timeline = page.locator(".timeline");
  const distance = () =>
    timeline.evaluate((el) => el.scrollHeight - el.clientHeight - el.scrollTop);
  await expect.poll(distance).toBeLessThan(4);
  await push(
    page,
    "Starting response.\n\n" +
      Array.from({ length: 12 }, (_, i) => `Line ${i}.`).join("\n\n"),
  );
  await expect.poll(distance).toBeLessThan(4);
  await timeline.hover();
  await page.mouse.wheel(0, -450);
  await expect(
    page.getByRole("button", { name: "↓ Jump to latest" }),
  ).toBeVisible();
  await page.waitForTimeout(150);
  const position = await timeline.evaluate((el) => el.scrollTop);
  await push(
    page,
    "Starting response.\n\n" +
      Array.from({ length: 24 }, (_, i) => `Line ${i}.`).join("\n\n"),
  );
  await page.waitForTimeout(450);
  expect(
    Math.abs((await timeline.evaluate((el) => el.scrollTop)) - position),
  ).toBeLessThan(5);
  await page.getByRole("button", { name: "↓ Jump to latest" }).click();
  await expect.poll(distance).toBeLessThan(4);
});

test("handles streamed tables, open code fences and large replacements without a backlog", async ({
  page,
}) => {
  await connect(page);
  const message = page.locator(".markdown-message");
  await push(page, "| Check | Result |\n| --- | --- |\n| Test |");
  await expect(message.locator("table")).toBeVisible();
  await expect(
    message.getByRole("cell", { name: "Test", exact: true }),
  ).toBeVisible();
  await push(page, "```go\nfunc main() {");
  await expect(message.locator("pre code")).toHaveText("func main() {\n");
  await expect(message.locator(".hljs-keyword")).toHaveText("func");
  await push(page, "$$\nx^2 + y^2");
  await expect(message.locator(".katex-display")).toBeVisible();
  const large = "Large update " + "content ".repeat(1000);
  await push(page, large);
  await expect(message).toHaveText(large, { timeout: 1500 });
  await push(page, "Replaced response", true);
  await expect(message).toHaveText("Replaced response");
});
