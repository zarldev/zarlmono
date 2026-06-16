# `zkit/skills`

Versioned, hot-reloadable cache of agent capabilities ("skills").
Each skill is a small struct holding a markdown body and metadata;
the store hands out the current set as a snapshot and bumps a
version counter on any change.

Not agent-specific: any system that builds prompts from versioned
text fragments can use it.

## Quick start

```go
store := skills.NewMemorySkillStore()

// Load the initial set (typically from a database / config file).
store.Load([]skills.Skill{
    {ID: "git", Name: "git", Markdown: "# Git\n..."},
    {ID: "search", Name: "search", Markdown: "# Search\n..."},
})

// Read for prompt assembly.
for _, s := range store.EnabledSkills() {
    // append s.Markdown to the prompt body
}

// On admin write, replace the cache and bump.
store.Load(updatedSkills)
// store.Version() has advanced; downstream caches see the change.
```

## Wiring an invalidation chain

When skills change, downstream caches (rendered prompts, tool
description overrides) often need to recompute. The store exposes
`AddBumper(InvalidationBumper)`:

```go
store.AddBumper(toolRegistry)   // *tools.Registry has BumpVersion()
store.AddBumper(promptCache)    // your prompt cache, same shape
```

On every `Load`, the store bumps its own version *and* calls
`BumpVersion()` on each registered bumper. Each downstream cache
keys off its own version field; no broadcast machinery needed.

## Key types

- `Skill` — the value: ID, Name, Description, Markdown, ProfileBinding.
- `SkillSource` — read interface (`EnabledSkills() []Skill`, `Version() int64`).
- `MemorySkillStore` — the in-memory cache (Load, EnabledSkills, Version, AddBumper).
- `InvalidationBumper` — single-method interface (`BumpVersion()`) for downstream caches.

See `AGENTS.md` for design notes.
