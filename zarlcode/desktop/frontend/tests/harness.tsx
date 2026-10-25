// Explicit test-only service; this entrypoint is excluded from production.
import { createRoot } from "react-dom/client";
import { App } from "../src/App";
import type { Snapshot, Transport, Command } from "../src/types";
import frames from "../src/replay.json";
import "../src/style.css";

const params = new URLSearchParams(location.search);
let snapshot: Snapshot = {
  revision: "0",
  sessionId: "test-session",
  workspace: "/test/project",
  provider: "controlled",
  model: "test-model",
  mode: "build",
  state: "ready",
  error: "",
  draft: params.has("draft") ? "Restored draft" : "",
  omitted: 0,
  entries: [],
};
const commands: Command[] = [];
let ticks = 0;
const api: Transport = {
  async connect(options) {
    if (params.has("unlock") && options.passphrase !== "test-password")
      throw new Error("vault passphrase required");
    if (params.has("history")) snapshot.entries = frames.at(-1)!.entries;
    if(params.has('rejected')||params.has('save-error')) {
      snapshot.state='unsaved'; snapshot.error='Checkpoint rejected';
      snapshot.retrySaveReason=params.has('rejected')?'checkpoint was rejected; retry cannot repair it':'';
    }
    if (params.has("route-error")) {
      snapshot.state = "route-error";
      snapshot.error = "No runnable target for the selected mode";
    }
    if (params.has("stream")) {
      snapshot.state = "running";
      snapshot.entries = [
        ...(params.has("scroll")
          ? Array.from({ length: 35 }, (_, i) => ({
              id: `old-${i}`,
              parentId: "",
              turnId: "old",
              kind: "assistant_message",
              payload: {
                text: `Archived response ${i}\n\nEarlier context for scroll stability.`,
                complete: true,
              },
            }))
          : []),
        {
          id: "stream",
          parentId: "",
          turnId: "task",
          kind: "assistant_message",
          payload: {
            text: params.has("scroll") ? "Starting response." : "This is **bo",
            complete: false,
          },
        },
      ];
    }
    if (params.has("features"))
      snapshot.entries = [
        {
          id: "features",
          parentId: "",
          turnId: "task",
          kind: "assistant_message",
          payload: {
            complete: true,
            text: [
              "## Formatting",
              "",
              "~~Removed~~ and **bold with *nested emphasis***.",
              "",
              "- Parent",
              "  - Child with `code`",
              "",
              "```go",
              'func main() { println("hello") }',
              "```",
              "",
              "```not-a-language",
              "keep this literal",
              "```",
              "",
              "Inline math $E = mc^2$.",
              "",
              "$$",
              "\\sum_{i=1}^{n} i = \\frac{n(n+1)}{2}",
              "$$",
              "",
              "A footnote[^note].",
              "",
              "[^note]: A local reference.",
              "",
              "$\\href{javascript:alert(1)}{unsafe}$",
              "",
              "$\\includegraphics{https://example.com/pixel}$",
            ].join("\n"),
          },
        },
      ];
    if (params.has("markdown"))
      snapshot.entries = [
        {
          id: "user",
          parentId: "",
          turnId: "task",
          kind: "user_message",
          payload: { text: "**Keep my input literal**" },
        },
        {
          id: "reply",
          parentId: "",
          turnId: "task",
          kind: "assistant_message",
          payload: {
            text: [
              "## Changes",
              "",
              "**Fixed** the `retry` loop.",
              "",
              "- bounded delay",
              "- cancellation",
              "",
              "> Keep it focused.",
              "",
              "| Check | Result |",
              "| --- | --- |",
              "| Tests | Pass |",
              "",
              "- [x] Verified",
              "",
              "```go",
              "if err != nil {",
              "    return err",
              "}",
              "```",
              "",
              "[Docs](https://example.com/docs)",
              "[Unsafe](javascript:alert%281%29)",
              "[Local](file:///etc/passwd)",
              "",
              "![tracking](https://example.com/pixel)",
              '<img src="https://example.com/raw" onerror="alert(1)">',
              "<script>alert(1)</script>",
              "",
              "```ts",
              'const partial = "still streaming',
            ].join("\n"),
          },
        },
      ];
    if (params.has("activity"))
      snapshot.entries = [
        {
          id: "u1",
          parentId: "",
          turnId: "task",
          kind: "user_message",
          payload: { text: "Fix the retry policy" },
        },
        {
          id: "a1",
          parentId: "",
          turnId: "task",
          kind: "assistant_message",
          payload: { text: "I’ll check the implementation and tests." },
        },
        {
          id: "t1",
          parentId: "",
          turnId: "task",
          kind: "tool_call",
          payload: {
            tool_name: "read",
            tool_state: "succeeded",
            argument: '{"path":"retry.go"}',
            effect: "source contents",
          },
        },
        {
          id: "t2",
          parentId: "",
          turnId: "task",
          kind: "tool_call",
          payload: {
            tool_name: "bash",
            tool_state: "failed",
            argument: '{"command":"go test"}',
            effect: "TestRetry failed",
          },
        },
        {
          id: "t3",
          parentId: "",
          turnId: "task",
          kind: "tool_call",
          payload: {
            tool_name: "edit",
            tool_state: "running",
            argument: '{"path":"retry.go"}',
          },
        },
        {
          id: "a2",
          parentId: "",
          turnId: "task",
          kind: "assistant_message",
          payload: { text: "The failing test points to an uncapped delay." },
        },
      ];
    if (params.has("large"))
      snapshot.entries = Array.from({ length: 10_000 }, (_, i) => ({
        id: `entry-${i}`,
        parentId: "",
        turnId: "task",
        kind: "assistant_message",
        payload: { text: `Archived message ${i}`, complete: true },
      }));
    if (params.has("cache"))
      snapshot.usage = {
        turns: 4,
        in: 1500,
        out: 320,
        cached: 600,
        cache_creation_tokens: 240,
        cache_diagnostics_version: 1,
        cache_targets: [
          {
            provider: "anthropic",
            model: "claude-sonnet",
            billing_class: "metered",
            settled_tasks: 3,
            root_tasks: 1,
            child_tasks: 2,
            prompt_tokens: 1000,
            completion_tokens: 300,
            cache_read_tokens: 600,
            cache_creation_tokens: 240,
            cache_creation_5m_tokens: 160,
            cache_creation_1h_tokens: 40,
            cache_creation_unknown_ttl: 40,
            cache_read_share: 0.6,
            cache_read_share_known: true,
            known_cost_usd: 0.0123,
            pricing_tier_unknown_tasks: 1,
            cache_read_rate_unknown_tasks: 2,
            cache_write_rate_unknown_tasks: 1,
          },
          {
            provider: "ollama",
            model: "qwen-local",
            billing_class: "local",
            settled_tasks: 1,
            child_tasks: 1,
            reported_zero_usage_tasks: 1,
          },
          {
            provider: "github-copilot",
            model: "gpt-subscription",
            billing_class: "subscription",
            settled_tasks: 1,
            root_tasks: 1,
            missing_usage_tasks: 1,
          },
        ],
      };
    if (params.has("legacy"))
      snapshot.usage = { turns: 2, in: 900, out: 100, cached: 400 };
    return structuredClone(snapshot);
  },
  async command(command) {
    commands.push(command);
    if(command.action==='export-recovery')snapshot.exportPath='/test/project/exports/recovery.md';
    if(command.action==='retry-save') { snapshot.state='ready';snapshot.error=''; }
    if (command.action === "draft") snapshot.draft = command.text!;
    if (command.action === "submit") {
      if (params.has("failure")) throw new Error("session changed elsewhere");
      snapshot = {
        ...snapshot,
        state: "running",
        draft: "",
        revision: String(Number(snapshot.revision) + 1),
        entries: [
          ...snapshot.entries,
          {
            id: `user-${commands.length}`,
            parentId: "",
            turnId: "task",
            kind: "user_message",
            payload: { text: command.text },
          },
        ],
      };
      ticks = 0;
    }
    if (command.action === "stop") snapshot.state = "cancelling";
    if (command.action === "mode") snapshot.mode = command.text!;
    if (command.action === "model") {
      snapshot.provider = command.provider!;
      snapshot.model = command.model!;
      if (snapshot.state === "route-error") {
        snapshot.state = "ready";
        snapshot.error = "";
      }
    }
    if (
      command.action === "snapshot" &&
      snapshot.state === "running" &&
      !params.has("hold") &&
      !params.has("stream")
    ) {
      ticks++;
      if (ticks === 2)
        snapshot = {
          ...snapshot,
          revision: "20",
          entries: [
            ...snapshot.entries,
            {
              id: "answer",
              parentId: "",
              turnId: "task",
              kind: "assistant_message",
              payload: { text: "Real streamed answer", complete: false },
            },
          ],
        };
      if (ticks > 3) {
        snapshot.state = "ready";
        snapshot.revision = "21";
      }
    }
    if (command.action === "snapshot" && snapshot.state === "cancelling")
      snapshot.state = "ready";
    return structuredClone(snapshot);
  },
};
Object.assign(window, {
  testCommands: commands,
  testStream: (text: string, done = false) => {
    const entry = snapshot.entries.find((e) => e.id === "stream")!;
    entry.payload.text = text;
    entry.payload.complete = done;
    snapshot.state = done ? "ready" : "running";
    snapshot.revision = String(Number(snapshot.revision) + 1);
  },
});
createRoot(document.getElementById("root")!).render(<App api={api} />);
