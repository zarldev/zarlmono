# AGENTS.md — `zarlcode/catalog`

Owns agent, skill, and hook discovery plus scaffolding.

## Discovery and precedence

- Keep discovery order stable: user config, canonical home/source locations, bounded source-family entries, then workspace-local entries.
- Later definitions override an earlier item with the same semantic name without changing its first-seen list position.
- Skills use the portable `<name>/SKILL.md` package layout. Other files in the package are resources, not independent skills.
- Agent and hook definitions are flat Markdown files with YAML frontmatter.
- A malformed file contributes a path-qualified error but must not hide other valid catalogue entries.
- The parsed agent `workspace` field is not enforced isolation. Do not advertise or rely on it until runner construction actually scopes the delegated tool source.

## Validation

- Require non-empty `name` and `description` fields.
- Agent modes are empty, `explore`, `verify`, or `implement`.
- Keep the runtime parser, scaffold templates, repository health check, and public documentation aligned when formats change.
- Never execute hook bodies during discovery; they are untrusted shell programs armed later by the engine.
- New persisted names are compatibility identifiers. Avoid renaming an existing agent, skill, hook event, or setting without an explicit migration.

## Testing

Use black-box catalogue tests with an isolated home directory and temporary workspace. Test precedence, malformed-file isolation, and absent-directory behavior.

```bash
go test -C zarlcode -count=1 ./catalog
```
