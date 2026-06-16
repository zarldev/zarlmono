# AGENTS.md — `zkit/skills`

Notes for editors. See [`zkit/agent/runner/AGENTS.md`](../agent/runner/AGENTS.md) for how a `PromptSource` consumes this store to assemble system prompts.

## What this package does

`MemorySkillStore` holds a versioned, hot-reloadable set of agent capabilities (skills). Each skill is a markdown guide injected into the LLM's system prompt when relevant. The store answers two questions: `EnabledSkills()` (what's the current set?) and `Version()` (have skills changed since I last read?).

## Why versioning instead of events

A skill change invalidates downstream caches (rendered prompts, description overrides, search indices). This package uses a monotonic version counter rather than an event broadcast because it composes with the runner's pull-based design: the runner doesn't subscribe to skill events; its `PromptSource` consults the source on every Run, sees the current version, and re-renders if it changed. No callback plumbing — just a counter and a lazy check. `AddBumper` is the explicit-push variant for caches that want to recompute *now* rather than on next read; both styles are supported.

## The Skill struct is intentionally slim

A `Skill` holds only what the runner uses: `ID`, `Name`, `Description`, `Markdown` (the canonical body), and `ProfileBinding` (empty = global). Persistence — timestamps, enabled flags, audit metadata — lives in the caller's repository, not here, so there's one source of truth.

## How Load works

`Load` replaces the entire skill set and increments the version exactly once. Bumpers fire *after* the lock is released, so a slow bumper doesn't block other readers/writers. All-or-nothing semantics keep the version contract simple and remove ordering ambiguity. To mutate partially, read with `EnabledSkills`, modify the slice, and write back with `Load`.

`EnabledSkills()` returns a fresh slice copy on every call: callers iterate without holding the store's lock, and the store can `Load` without invalidating in-flight readers. One allocation per read, negligible at skill scale.

## Things to never do

- **Don't store transport-shaped fields on `Skill`.** `Markdown` is canonical; render HTML or plain text from it at the edge. Don't add an `HTML` field.
- **Don't fire bumpers under the store's lock.** `Load` snapshots the bumper list under the lock, releases, then fires.
- **Don't add per-skill events.** The store tracks only that the set was replaced, not what changed. Stateful behaviour (enable/disable individual skills, audit logs) belongs in your repository layer.
