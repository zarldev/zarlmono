import { expect, test, type Page } from "@playwright/test";
async function diagram(page: Page, source: string, done = true) {
  await page.evaluate(
    ({ source, done }) =>
      (
        window as unknown as { testStream: (s: string, d: boolean) => void }
      ).testStream("```mermaid\n" + source + (done ? "\n```" : ""), done),
    { source, done },
  );
}
test.beforeEach(async ({ page }) => {
  await page.goto("/tests/harness.html?stream");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Prompt", exact: true }),
  ).toBeVisible();
});
for (const [name, source] of Object.entries({
  flowchart: "flowchart LR\n A[Prompt] --> B[Agent]\n B --> C[Tools]",
  sequence: "sequenceDiagram\n User->>Agent: Task\n Agent-->>User: Result",
  class: "classDiagram\n class Agent {\n +run()\n }",
  state: "stateDiagram-v2\n [*] --> Ready\n Ready --> Running",
  er: "erDiagram\n USER ||--o{ SESSION : owns",
  pie: 'pie title Results\n "Passed" : 8\n "Failed" : 2',
})) {
  test(`renders ${name} diagrams and exposes their source`, async ({
    page,
  }) => {
    await diagram(page, source);
    const image = page.getByRole("img", { name: "Mermaid diagram" });
    await expect(image).toBeVisible({ timeout: 15000 });
    await expect
      .poll(() => image.evaluate((el: HTMLImageElement) => el.naturalWidth))
      .toBeGreaterThan(0);
    await expect(
      page.locator(".mermaid-diagram svg, .mermaid-diagram iframe"),
    ).toHaveCount(0);
    await page.getByRole("button", { name: "Source", exact: true }).click();
    await expect(page.locator(".mermaid-diagram code")).toHaveText(source);
    await page.getByRole("button", { name: "Diagram", exact: true }).click();
    await expect(image).toBeVisible();
  });
}
test("handles incomplete and invalid diagrams without losing source or flashing parse errors", async ({
  page,
}) => {
  await diagram(page, "flowchart LR\n A[", false);
  await page.waitForTimeout(1000);
  await expect(
    page.getByText("Diagram could not be rendered", { exact: true }),
  ).toHaveCount(0);
  await diagram(page, "flowchart LR\n A[Ready] --> B[Complete]", true);
  await expect(page.getByRole("img", { name: "Mermaid diagram" })).toBeVisible({
    timeout: 15000,
  });
  await diagram(page, "not a valid diagram", true);
  await expect(
    page.getByText("Diagram could not be rendered", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".mermaid-diagram code")).toHaveText(
    "not a valid diagram",
  );
});
test("blocks diagram directives, external resources and HTML; diagram output never enters privileged DOM", async ({
  page,
}) => {
  const external: string[] = [];
  const dialogs: string[] = [];
  page.on("request", (r) => {
    if (
      !r.url().startsWith("http://127.0.0.1:4173/") &&
      !r.url().startsWith("data:image/svg+xml")
    )
      external.push(r.url());
  });
  page.on("dialog", async (d) => {
    dialogs.push(d.message());
    await d.dismiss();
  });
  for (const source of [
    '%%{init: {"securityLevel":"loose"}}%%\nflowchart LR\n A-->B',
    'flowchart LR\n A["<img src=x onerror=alert(1)>"]',
    'flowchart LR\n A-->B\n click A "https://example.com"',
  ]) {
    await diagram(page, source);
    await expect(
      page.getByText("Diagram could not be rendered", { exact: true }),
    ).toBeVisible();
    await expect(page.locator(".mermaid-diagram code")).toHaveText(source);
  }
  await expect(
    page.locator(
      ".mermaid-diagram svg,.mermaid-diagram iframe,.mermaid-diagram script",
    ),
  ).toHaveCount(0);
  expect(dialogs).toEqual([]);
  expect(external).toEqual([]);
});
