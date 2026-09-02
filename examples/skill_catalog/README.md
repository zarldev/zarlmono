# Skill catalog

A deterministic, filesystem-only example of the skill lifecycle:

1. create a portable `.zarlcode/skills/<name>/SKILL.md` package in a temporary workspace;
2. discover it with `catalog.LoadSkills`;
3. convert the catalog records to `skills.Skill` values and atomically `Load` them into `MemorySkillStore`;
4. read a snapshot and its monotonic version.

The example replaces `HOME` with a temporary directory while discovering, so installed user skills cannot affect its output. It removes all temporary files before exiting. It makes no network or LLM calls.

Run from the repository root:

```sh
go run -C examples ./skill_catalog
```

Verify:

```sh
go test -C examples ./skill_catalog
```
