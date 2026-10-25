import { expect, test, type Page } from "@playwright/test";

async function connect(page: Page, query = "") {
  await page.goto(`/tests/harness.html${query}`);
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Prompt", exact: true }),
  ).toBeVisible();
}

test("actual frontend never falls back to replay in a browser", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Open the native workbench",
  );
  await expect(page.getByRole("slider")).toHaveCount(0);
  await expect(page.getByText("REPLAY", { exact: true })).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: /Play|Reset|Step/ }),
  ).toHaveCount(0);
});

test("connects, submits a prompt, receives updates and admits a follow-up", async ({
  page,
}) => {
  await connect(page);
  await expect(page.locator(".placeholder-line, .placeholder-row")).toHaveCount(
    0,
  );
  await expect(page.locator(".inspector-body")).toHaveText("");
  await expect(page.locator(".timeline")).toHaveText("");
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeDisabled();
  await page
    .getByRole("textbox", { name: "Prompt", exact: true })
    .fill("Fix the bug");
  await page.getByRole("button", { name: "Send ↗" }).click();
  await expect(page.getByRole("button", { name: "■ Stop" })).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "Prompt", exact: true }),
  ).toHaveValue("");
  await expect(
    page.getByText("Real streamed answer", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeVisible();
  await page
    .getByRole("textbox", { name: "Prompt", exact: true })
    .fill("Add tests");
  await page
    .getByRole("textbox", { name: "Prompt", exact: true })
    .press("Control+Enter");
  await expect(page.getByText("Add tests", { exact: true })).toBeVisible();
});

test("stops active work and preserves rejected input", async ({ page }) => {
  await connect(page, "?hold");
  await page
    .getByRole("textbox", { name: "Prompt", exact: true })
    .fill("Long task");
  await page.getByRole("button", { name: "Send ↗" }).click();
  await page.getByRole("button", { name: "■ Stop" }).click();
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeVisible();
  await connect(page, "?failure");
  await page
    .getByRole("textbox", { name: "Prompt", exact: true })
    .fill("Keep this draft");
  await page.getByRole("button", { name: "Send ↗" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "session changed elsewhere",
  );
  await expect(
    page.getByRole("textbox", { name: "Prompt", exact: true }),
  ).toHaveValue("Keep this draft");
});

test("unlocks without retaining the passphrase and switches mode/model", async ({
  page,
}) => {
  await page.goto("/tests/harness.html?unlock");
  await page.getByLabel("Vault passphrase").fill("wrong");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(page.getByLabel("Vault passphrase")).toHaveValue("");
  await page.getByLabel("Vault passphrase").fill("test-password");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await page.getByLabel("Mode", { exact: true }).selectOption("plan");
  await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("plan");
  await page.getByRole("button", { name: "Model settings" }).click();
  await page.getByLabel("Provider", { exact: true }).fill("local");
  await page.getByLabel("Model", { exact: true }).fill("coder");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.locator(".top-context")).toHaveText("local / coder");
});

test("route errors block submit while allowing manual mode and model recovery", async ({ page }) => {
  await connect(page, "?route-error");
  const prompt = page.getByRole("textbox", { name: "Prompt", exact: true });
  await prompt.fill("must not submit without a route");
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeDisabled();

  const mode = page.getByLabel("Mode", { exact: true });
  await expect(mode).toBeEnabled();
  await mode.selectOption("plan");
  await expect(mode).toHaveValue("plan");

  await page.getByRole("button", { name: "Model settings" }).click();
  await page.getByLabel("Provider", { exact: true }).fill("local");
  await page.getByLabel("Model", { exact: true }).fill("coder");
  const apply = page.getByRole("button", { name: "Apply", exact: true });
  await expect(apply).toBeEnabled();
  await apply.click();
  await expect(page.locator(".top-context")).toHaveText("local / coder");
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeEnabled();
});

test("shows truthful per-model cache diagnostics for mixed billing targets", async ({ page }) => {
  await connect(page, "?cache");
  const diagnostics = page.locator(".cache-diagnostics");
  await expect(diagnostics.locator("summary")).toContainText("3 models");
  await diagnostics.locator("summary").click();
  await expect(diagnostics.getByText("anthropic / claude-sonnet", { exact: true })).toBeVisible();
  await expect(diagnostics.getByText("Local · no API billing", { exact: true })).toBeVisible();
  await expect(diagnostics.getByText("Subscription · no metered API price", { exact: true })).toBeVisible();
  await expect(diagnostics.getByText("60.0% of reported prompt tokens", { exact: true })).toBeVisible();
  await expect(diagnostics.getByText("Unavailable · zero prompt-token denominator", { exact: true })).toHaveCount(2);
  await expect(diagnostics).toContainText("Prompt pricing tier unavailable for 1 task(s); the base-rate estimate is incomplete.");
  await expect(diagnostics.getByText(/40 unknown TTL/)).toBeVisible();
  await expect(diagnostics.getByText("Cache pricing unavailable: 2 read task(s), 1 write task(s).", { exact: true })).toBeVisible();
  await expect(diagnostics.locator(".cache-subtotal strong")).toHaveText("$0.0123");
  await expect(diagnostics).toContainText("not cache-hit causality");
  await expect(diagnostics).not.toContainText(/saved dollars?[^.]*\$/i);
});

test("labels legacy usage as unattributed without requiring new snapshot fields", async ({ page }) => {
  await connect(page, "?legacy");
  const diagnostics = page.locator(".cache-diagnostics");
  await expect(diagnostics.getByText("No attributed tasks", { exact: true })).toBeVisible();
  await diagnostics.locator("summary").click();
  await expect(diagnostics).toContainText(
    "Legacy unattributed history: earlier usage cannot be assigned to a provider/model.",
  );
});

test("inspects real-shaped tool/diff evidence and treats output as inert text", async ({
  page,
}) => {
  const dialogs: string[] = [];
  page.on("dialog", async (d) => {
    dialogs.push(d.message());
    await d.dismiss();
  });
  await connect(page, "?history");
  await page.getByRole("button", { name: "M internal/retry/retry.go" }).click();
  await expect(page.locator("pre.diff")).toContainText("case <-ctx.Done():");
  await page.getByRole("button", { name: "Changes 1", exact: true }).click();
  await page.getByRole("button", { name: "Evidence", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Filter conversation" })
    .fill("no match anywhere");
  await expect(
    page.getByRole("status").filter({ hasText: "No matches" }),
  ).toBeVisible();
  await connect(page, "?history");
  await page.locator(".evidence-list button").first().click();
  await expect(page.locator(".output-block")).toContainText("<img src=x");
  await expect(page.locator("img")).toHaveCount(0);
  expect(dialogs).toEqual([]);
});

test("virtualizes history and fits the minimum native window", async ({
  page,
}) => {
  await page.setViewportSize({ width: 860, height: 640 });
  await connect(page, "?large");
  await expect(page.getByText("10,000 entries", { exact: true })).toBeVisible();
  expect(await page.getByTestId("transcript-entry").count()).toBeLessThan(60);
  await expect(page.getByRole("button", { name: "Send ↗" })).toBeInViewport();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(860);
});

test("restores drafts and synchronizes edits before submitting", async ({
  page,
}) => {
  await connect(page, "?draft");
  const composer = page.getByRole("textbox", { name: "Prompt", exact: true });
  await expect(composer).toHaveValue("Restored draft");
  await composer.fill("Revised draft");
  await page.getByRole("button", { name: "Send ↗" }).click();
  await expect(composer).toHaveValue("");
  const actions = await page.evaluate(() =>
    (
      window as unknown as { testCommands: { action: string; text?: string }[] }
    ).testCommands.filter((c) => c.action !== "snapshot"),
  );
  expect(actions.slice(0, 2)).toEqual([
    { action: "draft", text: "Revised draft" },
    { action: "submit", text: "Revised draft" },
  ]);
});

test("keeps tool bursts compact without hiding failures or evidence", async ({
  page,
}) => {
  await connect(page, "?activity");
  await expect(page.getByTestId("transcript-entry")).toHaveCount(3);
  const group = page.getByTestId("activity-group");
  await expect(group).toHaveCount(1);
  const toggle = group.getByRole("button", {
    name: "3 tool calls · running · 1 failed",
    exact: true,
  });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect((await group.boundingBox())!.height).toBeLessThan(40);
  await expect(page.locator(".timeline .tool-summary")).toHaveCount(0);
  await expect(page.locator(".timeline pre")).toHaveCount(0);
  await toggle.click();
  await expect(group.locator(".activity-item")).toHaveCount(3);
  await group.getByRole("button", { name: "bash failed", exact: true }).click();
  await expect(page.locator(".output-block")).toHaveText("TestRetry failed");
  await toggle.click();
  await expect(group.locator(".activity-item")).toHaveCount(0);
  await expect(
    page.getByText("The failing test points to an uncapped delay.", {
      exact: true,
    }),
  ).toBeVisible();
});

test("keeps delegated prose out of the main thread until the agent is selected", async ({
  page,
}) => {
  await connect(page, "?history");
  await expect(
    page
      .locator(".timeline .message-text")
      .filter({ hasText: "Checking existing tests" }),
  ).toHaveCount(0);
  await page.locator(".agent.child").first().click();
  await expect(
    page
      .locator(".timeline .message-text")
      .filter({ hasText: "Checking existing tests" }),
  ).toBeVisible();
});

test("renders assistant Markdown without executing HTML or fetching images", async ({
  page,
}) => {
  const external: string[] = [];
  const dialogs: string[] = [];
  page.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:4173/"))
      external.push(request.url());
  });
  page.on("dialog", async (dialog) => {
    dialogs.push(dialog.message());
    await dialog.dismiss();
  });
  await connect(page, "?markdown");
  const markdown = page.locator(".markdown-message");
  await expect(
    markdown.getByRole("heading", { name: "Changes" }),
  ).toBeVisible();
  await expect(markdown.locator("strong")).toHaveText("Fixed");
  await expect(markdown.locator("ul").first().locator("li")).toHaveCount(2);
  await expect(markdown.locator("blockquote")).toContainText(
    "Keep it focused.",
  );
  await expect(
    markdown.getByRole("cell", { name: "Pass", exact: true }),
  ).toBeVisible();
  await expect(markdown.getByRole("checkbox")).toBeChecked();
  await expect(markdown.getByRole("checkbox")).toBeDisabled();
  await expect(markdown.locator("pre code.language-go")).toHaveText(
    "if err != nil {\n    return err\n}\n",
  );
  await expect(markdown.locator("pre code.language-ts")).toContainText(
    "still streaming",
  );
  await expect(markdown.getByRole("link", { name: "Docs" })).toHaveAttribute(
    "href",
    "https://example.com/docs",
  );
  await expect(markdown.getByRole("link", { name: "Unsafe" })).toHaveCount(0);
  await expect(markdown.getByRole("link", { name: "Local" })).toHaveCount(0);
  await expect(markdown.locator("img,script,iframe")).toHaveCount(0);
  await expect(page.locator(".user_message .message-text")).toHaveText(
    "**Keep my input literal**",
  );
  expect(external).toEqual([]);
  expect(dialogs).toEqual([]);
});
