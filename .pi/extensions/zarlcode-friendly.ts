import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

async function listMarkdownSummaries(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".md"))
    .map((entry) => entry.name)
    .sort();

  const summaries = await Promise.all(
    files.map(async (file) => {
      const path = join(dir, file);
      const text = await readFile(path, "utf8");
      const title = text.split("\n").find((line) => line.startsWith("# "))?.replace(/^#\s+/, "") ?? file;
      return `- ${file.replace(/\.md$/, "")}: ${title}`;
    }),
  );

  return summaries;
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.setWidget("zarlmono", [
      "zarlmono helpers: /wr, /review, /test, /research, /handover",
      "Skills: /skill:go-style plus focused Go/topic skills",
      "Commands: /agents, /repo",
    ]);
  });

  pi.registerCommand("agents", {
    description: "List zarlcode delegated agent profiles",
    handler: async (_args, ctx) => {
      try {
        const summaries = await listMarkdownSummaries(".zarlcode/agents");
        ctx.ui.notify(["zarlcode agent profiles:", ...summaries].join("\n"), "info");
      } catch (error) {
        ctx.ui.notify(`agents list error: ${error instanceof Error ? error.message : String(error)}`, "error");
      }
    },
  });

  pi.registerCommand("repo", {
    description: "Show zarlmono module and verification cheat sheet",
    handler: async (_args, ctx) => {
      ctx.ui.notify(
        [
          "zarlmono modules:",
          "- zkit/: shared runner/tools/providers foundation",
          "- zarlcode/: terminal coding-agent TUI/CLI",
          "- swebench-eval/: SWE-bench driver",
          "- examples/: runnable harness examples",
          "",
          "Verification:",
          "- go tool task check",
          "- go tool task lint",
          "- go tool task race",
          "- go test -C <module> ./... for focused module tests",
        ].join("\n"),
        "info",
      );
    },
  });
}
