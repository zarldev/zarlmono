# zarl.dev growth plan: zarlai → zkit → zarlcode

This is the working plan for turning the zarlai / zkit / zarlcode story into a user-acquisition push.

Goal: **drive users**, especially for `zarlcode`, without sounding like a generic coding-agent launch.

The route is a technical zarl.dev writeup that tells the real engineering story:

> Build zarlai → learn the hard lessons → design zkit/zarlkit → build zarlcode → retrofit the improved substrate back into zarlai.

`zarlcode` is the conversion target. `zkit` is the credibility and technical substance. `zarlai` is the origin story.

---

## 1. Core positioning

### The narrative

The story is not:

> I built another AI coding agent.

The story is:

> I built a local AI assistant, learned where agent systems hurt, designed a Go agent toolkit from those lessons, built my daily coding agent on it, and am now feeding the cleaner substrate back into the assistant.

That is more credible, more interesting, and less self-promotional.

### The short version

> **zarlai taught me the problems. zkit is the learned architecture. zarlcode is the daily-driver proof. The zarlai retrofit closes the loop.**

### The strongest single sentence

> **zkit was not me trying to invent an agent framework. It was me trying not to solve the same hard problems badly twice.**

### Product roles

| Project | Role in the story | What it proves |
|---|---|---|
| `zarlai` | messy real-world assistant/product | voice, camera, local models, Home Assistant, MCP, memory, background tasks, UI events expose real agent pain |
| `zkit` / `zarlkit` | designed reusable substrate | the lessons became Go packages and contracts rather than one-off app code |
| `zarlcode` | narrower daily-driver coding product | the substrate works in a second serious product, not just the original assistant |
| zarlai retrofit | validation loop | abstractions that survive zarlcode improve the original assistant too |

---

## 2. Audience

Primary audience:

- Go developers interested in agents, tools, local/open models, or coding agents.
- Developers building or evaluating LLM products.
- Terminal-heavy engineers who want more control than Claude Code / Codex-style product surfaces.

Secondary audience:

- People curious about local AI assistants.
- People building Home Assistant / MCP / local model integrations.
- People interested in evals and model switching.

Avoid generic “AI coding assistant” messaging. That space is crowded and the default comparison is Claude Code.

The more specific wedge is:

> zarlcode is useful when you want to flip models, run eval-like coding tasks, inspect the loop, and keep the session local/resumable.

---

## 3. zarlcode wedge

The confirmed real-world wedge:

> Steven uses zarlcode over Claude Code daily at work against an open-LLM core product because zarlcode makes it much easier to flip models and run evals.

This is the retention proof. Lead with it when pitching zarlcode.

Important zarlcode differentiators:

- model/provider switching from the TUI
- easier model/eval workflow than Claude Code for open-LLM work
- local/resumable sessions via `-continue`
- Plan/Build mode split
- visible tool calls and command output
- workspace-scoped file and shell tools
- sub-agents
- headless mode for scriptable tasks/evals
- inline sudo / askpass flow so sudo prompts do not force exiting the TUI
- local providers: Ollama / llama.cpp / OpenAI-compatible backends

Messaging hierarchy:

1. model flipping + eval ergonomics
2. local/resumable sessions
3. visible/guarded tool calls
4. Plan/Build mode
5. sub-agents/headless workflows

Do not lead with “it edits code” or “AI coding agent”. That is table stakes.

---

## 4. Article strategy

### Working title options

Best options:

1. **From zarlai to zarlcode: designing a Go agent toolkit the hard way**
2. **I built a local AI assistant, learned the hard parts, then designed zkit**
3. **The agent toolkit I wish I had before building zarlai**
4. **zarlai taught me the problems; zarlcode proved the toolkit**
5. **A Go agent toolkit shaped by two real products**

Recommended title:

> **From zarlai to zarlcode: designing a Go agent toolkit the hard way**

It has the full arc, avoids sounding like a product page, and sets up a technical post.

### Article thesis

> zarlai started as a local HAL-style assistant. It worked, but it exposed the painful parts of building agent systems: model providers, tools, schemas, context pressure, long-running tasks, MCP, memory, background events, guardrails, evals, and local model ergonomics. zkit is the designed response to those lessons. zarlcode is the coding-agent product built on the cleaner substrate, and the pieces that survive zarlcode are now being retrofitted back into zarlai.

---

## 5. Article outline

### 1. Open with the previous zarlai post

Reference the existing zarl.dev post:

- “A HAL by any other name is still a HAL”
- local assistant
- voice loop
- camera / multimodal input
- Home Assistant
- MCP tools
- local search
- timers/reminders
- background research tasks
- avatar/gestures

Opening draft:

```md
In the last post I wrote about zarlai, my local HAL experiment: voice, camera, Home Assistant, MCP tools, local search, reminders, background research tasks, and a talking avatar that gestures when the model decides it should.

That version worked, but it also taught me where the pain actually lives.

The hard part was not rendering an avatar or wiring up a chat box. The hard part was everything underneath: model providers, tool schemas, context management, long-running tasks, background events, guardrails, eval loops, and making local/open models feel usable rather than bolted on.

So I took the lessons from zarlai and designed zkit.

Then I built zarlcode on top of it.

Now the useful bits are going back into zarlai.
```

### 2. What zarlai taught me

Make this section concrete and slightly messy. The point is that zarlai was a real product-shaped experiment, not a toy example.

Lessons list:

- model providers need to be swappable
- local/open models need different ergonomics from API models
- tools need schemas and clear contracts, not prompt-only vibes
- tool surfaces get messy fast
- context rot is real
- long-running conversations need compaction
- background tasks are not the same as foreground chat
- MCP is useful but needs boundaries
- memory/retrieval can pollute context if not disciplined
- eval/re-drive loops matter once prompts and tools start changing
- event streams and notifications matter for UIs
- one-off agent code becomes unmaintainable
- Claude-generated code can work but needs guardrails and cleanup

Possible section framing:

```md
zarlai was not a clean architecture exercise. It was a pile of real requirements: voice, camera, local models, Home Assistant, MCP, memory, background tasks, gestures, and a UI that had to react to all of it. That mess taught me where the reusable seams actually were.
```

### 3. Designing zkit / zarlkit

Define zkit clearly:

> zkit is the reusable Go substrate for agent runtimes and surrounding infrastructure.

Do not call it a giant framework. Emphasize ordinary Go packages and interfaces.

Important packages/concepts to mention:

- `agent/runner` — tool-calling loop, events, iterations, lifecycle
- `ai/llm` — provider abstraction
- `ai/tools` — tool contracts, schemas, registry
- `agent/guardrails` — validation/policy around dispatch
- `agent/compact` — keeping long sessions inside context
- `agent/pursue` — deterministic re-drive/eval harness
- `agent/sandbox` — safer command execution boundaries
- `agent/tools/spawn` — sub-agent dispatch
- `mcp` — MCP client/server bridges
- `messagebus` / notifications — eventing patterns useful for zarlai
- `vectorstore` / retrieval — memory/search substrate

Key explanation:

```md
zkit was not designed in a vacuum. It came after building the messy version once. The goal was not to hide everything behind a framework DSL. The goal was to make the boring pieces explicit enough that two different products could share them.
```

### 4. Building zarlcode as proof

Introduce zarlcode as the cleaner second product:

```md
zarlcode was the first clean product built on the new substrate.

It takes the same core problems — tools, context, model providers, long-running sessions, guardrails — and narrows the domain to coding.
```

Mention:

- terminal TUI/CLI
- workspace-scoped file tools
- shell commands
- web search/fetch
- MCP tools
- sub-agents
- Plan/Build mode
- persisted local sessions
- `zarlcode -continue`
- headless mode
- model picker / provider switching
- inline sudo askpass

Important daily-driver paragraph:

```md
The reason I use it over Claude Code day to day is not that Claude Code is bad. It is that my work involves flipping models and running eval-ish coding tasks against an open-LLM product, and zarlcode makes that loop cheap. Switching models is part of the workflow, not a side quest.
```

### 5. Retrofitting the lessons back into zarlai

This is the validation section.

```md
The nice part is that the work did not stop at zarlcode. The patterns that survived zarlcode are now going back into zarlai.

That is the validation step. If a piece only works for the coding agent, it belongs in zarlcode. If it makes zarlai cleaner too, it probably belongs in zkit.
```

Explain that this closes the loop:

- zarlai revealed the pain
- zkit encoded lessons
- zarlcode stress-tested the design
- zarlai gets cleaner as the substrate comes back

### 6. Browser-runnable zkit loop demo

Optional but valuable. Keep it scoped.

Goal:

> Let readers feel the zkit runner/tool loop before installing anything.

Do **not** build a generic Go playground. Existing options already exist: Yaegi/WASM, GopherJS, Go WASM, LiveCodes-style playgrounds.

Best staged approach:

1. ship static/interactive trace replay first
2. experiment with Yaegi/WASM or native Go WASM later
3. avoid live LLM/API-key execution in the browser for v1

Demo concept:

```text
Prompt: inspect this tiny project

assistant → tool_call list_files {}
tool → ["go.mod", "main.go", "main_test.go"]

assistant → tool_call run_tests {}
tool → ok example 0.13s

assistant → tests pass; project looks healthy
```

Adjacent code snippet:

```go
r := runner.New(
    runner.ClientFromProvider(scripted),
    runner.WithTools(tools.NewRegistry(listFiles{}, runTests{})),
)
```

CTA below the widget:

> Want this against your actual repo? Try zarlcode.

Implementation options:

#### Option A: interactive trace replay

Recommended first version.

- generate a JSON trace from an existing deterministic example
- render events in zarl.dev
- add play/pause/step/reset
- show code side-by-side
- no runtime risk
- no API keys
- no WASM dependency fight

Label honestly:

> Replay a real zkit run.

#### Option B: Yaegi/WASM

Use existing browser-Go work as a base/inspiration.

Pros:

- editable Go snippets
- no backend
- nice educational feel

Cons:

- full zkit imports may not work cleanly
- best for a mini zkit-style educational loop, not necessarily the real module

#### Option C: native Go WASM

Compile a tiny demo:

```bash
GOOS=js GOARCH=wasm go build -o demo.wasm ./cmd/zkit-demo-wasm
```

Pros:

- real Go code
- great story if it works

Risks:

- browser-incompatible dependencies
- filesystem/process/network/signal assumptions
- larger bundle

Only attempt with a tiny dependency slice: fake provider + in-memory tools + runner events.

#### Option D: backend sandbox

Last resort if real imports are needed.

Pros:

- easiest to run arbitrary real Go server-side

Cons:

- hosting cost
- abuse prevention
- rate limiting
- security concerns

Avoid for v1.

### 7. Closing / CTAs

End with direct but not pushy CTAs.

Suggested ending:

```md
If you want the coding agent, try zarlcode:

    go install github.com/zarldev/zarlmono/zarlcode/cmd@latest

If you want the reusable Go pieces, start with zkit and the examples:

    github.com/zarldev/zarlmono/zkit
    github.com/zarldev/zarlmono/examples

And if you want the weird local HAL that started all of this, the previous zarlai post is here:

    [A HAL by any other name is still a HAL](...)
```

Also mention Homebrew install if the tagged release supports it:

```bash
brew install zarldev/tap/zarlcode
```

---

## 6. Assets to use

Existing repo assets:

- `zarlcode/docs/images/hero.gif`
- `zarlcode/docs/images/screen-fileviewer.gif`
- `zarlcode/docs/images/screen-modelpicker.gif`
- `zarlcode/docs/images/screen-subagents.gif`
- `zarlcode/docs/images/screen-workingset.gif`
- `zarlcode/docs/images/screen-planmode.gif`
- `zarlcode/docs/images/screen-cockpit.gif`
- `site/public/zarlcode-hero2.gif`
- `site/public/zarlcode-modelpicker.gif`
- `site/public/zarlcode-planmode.gif`
- `site/public/zarlcode-subagents.gif`
- `site/public/zarlcode-cockpit.gif`

Need one new narrative clip if possible:

> real task → switch model → run eval/headless task → inspect output/diff → inline sudo without leaving TUI

If not enough time, use existing `screen-modelpicker.gif` plus `hero.gif` and keep the article technical.

---

## 7. README/site changes to pair with the article

Before posting widely, adjust top-level conversion paths.

### Root README

Add or strengthen:

- zarlai taught the lessons
- zkit is the reusable substrate
- zarlcode is the coding agent built on it
- quick install command for zarlcode

### zarlcode README

Lead with:

- model/provider switching
- daily-driver use for eval/open-LLM workflows
- local/resumable sessions
- Plan/Build mode
- visible tool calls

Add a “first 3 minutes” block:

```bash
go install github.com/zarldev/zarlmono/zarlcode/cmd@latest
zarlcode init
zarlcode keys set <provider>
zarlcode
```

Also include local-provider path if smooth:

```bash
# with Ollama already running
zarlcode
```

Do not overpromise zero-config local launch unless tested end-to-end.

### zkit README

Lead with:

> The Go toolkit extracted from zarlai and used by zarlcode.

Add:

- one minimal runner/tools example
- links to examples
- note that it is ordinary Go packages, not a framework DSL

---

## 8. Distribution plan

r/golang direct self-promo is deferred for now because a prior attempt was blocked for account/history/moderation reasons.

Primary distribution:

1. publish on zarl.dev
2. post to Hacker News if the article is technical and useful enough
3. share on personal channels
4. submit to Go newsletters, especially Golang Weekly
5. share in communities where there is existing participation
6. later return to r/golang after more normal participation, framed as a technical writeup, not a product launch

HN title options:

- **From zarlai to zarlcode: designing a Go agent toolkit the hard way**
- **I built a local AI assistant, learned the hard parts, then designed a Go agent toolkit**
- **zkit: a Go agent toolkit shaped by a local assistant and a coding agent**

Preferred HN title:

> **From zarlai to zarlcode: designing a Go agent toolkit the hard way**

HN submission note should be short, personal, and not salesy:

```text
I wrote up the path from my local assistant project (zarlai) to zkit, the Go agent substrate that now also powers my terminal coding agent (zarlcode). The main lessons were around tool contracts, context management, local/open model ergonomics, compaction, guardrails, and eval/model-switching workflows.
```

---

## 9. Success metrics

Do not only watch pageviews.

Track if possible:

- zarl.dev post views
- GitHub repo visits/stars
- zarlcode install clicks / Homebrew / release downloads
- docs clicks from article
- `zarlcode -continue` usage if telemetry exists or can be inferred locally/anonymously later
- issues/discussions opened
- newsletter/community referrals

Most important qualitative signal:

> Are people asking about zarlcode as a daily tool or zkit as a Go substrate?

If everyone only comments on the local HAL/avatar, the article needs stronger zkit/zarlcode bridge CTAs.

---

## 10. Execution checklist

### Phase 1: article draft

- [ ] Generate headings first; do **not** draft the full post in LLM prose.
- [ ] Use speech-to-text to talk through each heading in Steven's own words.
- [ ] Use LLM only for cleanup/structure after the spoken draft exists.
- [ ] Run cleanup against `VOICE.md` when available so it avoids phrasing/style Steven dislikes.
- [ ] Link the existing zarlai post early.
- [ ] Include concrete package names and code snippets.
- [ ] Include the daily-driver zarlcode paragraph.
- [ ] Include honest caveats: young project, APIs may move, local setup still has rough edges.
- [ ] Final human pass before publishing; the post should sound like Steven, not an LLM.

### Phase 2: demo/widget

- [ ] Decide v1: trace replay unless WASM is obviously easy.
- [ ] Generate or mock a deterministic agent event trace.
- [ ] Build simple play/step/reset UI.
- [ ] Add CTA: “Want this against your repo? Try zarlcode.”

### Phase 3: conversion polish

- [ ] Update zarlcode README top.
- [ ] Update zkit README top.
- [ ] Check install commands and latest tags.
- [ ] Verify local/Ollama path before claiming zero-key first run.
- [ ] Ensure existing GIFs render correctly from article.

### Phase 4: publish and distribute

- [ ] Publish on zarl.dev.
- [ ] Submit to HN.
- [ ] Share on personal channels.
- [ ] Submit to Go newsletters.
- [ ] Share in relevant communities with context.
- [ ] Defer r/golang until there is more normal participation.

---

## 11. Things to avoid

- Do not pitch “another coding agent”.
- Do not lead with comparison pages.
- Do not spend weeks building a generic Go playground.
- Do not start with browser API-key execution.
- Do not over-focus on REDRIVE/SaaS strategy; this plan is for user acquisition.
- Do not make the post too clean. The messy zarlai lessons are the credibility.
- Do not overpromise local zero-config unless tested.

---

## 12. Final message

The growth asset is not a product page. It is a technical story with a real arc:

> I built zarlai, learned the hard parts of real agent systems, designed zkit from those lessons, built zarlcode on top, and now the better substrate is going back into zarlai.

That story gives readers a reason to care about zkit, and zkit gives them a reason to believe zarlcode is more than another coding-agent wrapper.
