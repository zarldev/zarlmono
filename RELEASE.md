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

## Tagging

### zkit (shared library) — required

```bash
git tag -a zkit/vX.Y.Z -m "zkit vX.Y.Z — <summary>"git push origin zkit/v0.1.0
```

### zarlcode (TUI product) — required

```bash
go tool task zarlcode:release VERSION=vX.Y.Z
```

This creates `zarlcode/vX.Y.Z`, validates the version format, checks for a clean tree, and pushes the tag. The Taskfile task is the canonical path — it also ensures the `zarlcode` binary's ldflags version string matches.

### swebench-eval (eval tool) — optional but recommended

```bash
git tag -a swebench-eval/vX.Y.Z -m "swebench-eval vX.Y.Z — <summary>"git push origin swebench-eval/v0.1.0
```

### zarlai (assistant app) — defer until stable

`zarlai` is excluded from standard CI and has CGO system dependencies. No release pipeline exists yet. Tag when:
- CI job for `zarlai` is added (or the exclusion is justified)
- A `task zarlai:release` is added to `Taskfile.yml`
- The binary builds reliably in CI environments

## Post-release

- [ ] Push CHANGELOG.md
- [ ] Verify tags on GitHub: `git tag -l | sort`
- [ ] Update GitHub Releases page with the release body (copy from CHANGELOG)
- [ ] If `HOMEBREW_TAP_TOKEN` is configured in GitHub Actions, verify `zarldev/homebrew-tap` updated with the new `Formula/zarlcode.rb`
- [ ] Announce in relevant channels (Discord, etc.)

## Versioning rules

- **Pre-v1**: APIs may evolve. Stable-tier packages should avoid breaking changes.
- **Tag format**: `module/vX.Y.Z` — the module name is the prefix, not the repo name.
- **Go module consumers** use `go get github.com/zarldev/zarlmono/zkit@v0.1.2` to pin.
- **zarlcode** consumers use `go install github.com/zarldev/zarlmono/zarlcode/cmd@v0.1.2` or `zarlcode upgrade`.

## Next release (v0.2.0)
When v0.2.0 is ready:
1. Bump version in CHANGELOG.md
2. Run the checklist above
3. Consider whether any beta packages have matured to shared/stable tier
4. Consider adding `zarlai` to the standard CI matrix if CGO deps are resolved
