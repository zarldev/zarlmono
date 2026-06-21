# Release Checklist

Use this checklist before each release.

## Pre-release

- [ ] `go tool task check` — build + vet + test passes on all CI modules
- [ ] `go tool task lint` — golangci-lint passes on all CI modules
- [ ] `go tool task race` — zkit race tests pass
- [ ] Working tree is clean (`git status` shows nothing)
- [ ] CHANGELOG.md updated with all changes since last release
- [ ] Date in CHANGELOG.md is correct
- [ ] No `go get -u` was run (which would re-bump `jsonschema` past v0.13.0)

## Release button (canonical path)

Use the GitHub Actions **release-dispatch** workflow as the operator-facing release button.

Inputs:
- `version`: `vX.Y.Z`
- `scope`: direct choices for `zkit`, `zarlcode`, `zarlai`, `swebench-eval`, `examples`, plus `all` and `custom`
- `custom_modules`: comma-separated list for `scope=custom`
- `mode`: `dry-run` or `publish`

The workflow validates the version, checks that `CHANGELOG.md` contains the target release, verifies tags do not already exist, runs module build preflights, and either prints the plan (`dry-run`) or creates/pushes the tags (`publish`).3. Consider whether any beta packages have matured to shared/stable tier
4. Consider adding `zarlai` to the standard CI matrix if CGO deps are resolved
