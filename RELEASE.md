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

The workflow validates the version, checks that `CHANGELOG.md` contains the target release, verifies tags do not already exist, runs module build preflights, and either prints the plan (`dry-run`) or creates/pushes the tags (`publish`).

## Coordinated internal-module releases

When zkit and consumers change together, publish in dependency order rather than tagging every module from one commit while consumers still pin an older zkit:

1. Merge a clean preparation commit containing the zkit changes and its changelog entry.
2. Run release-dispatch with `scope=zkit`, `mode=dry-run`, then publish the zkit tag.
3. Update consumer `go.mod` files to the published zkit version; run `go mod tidy` in each affected module and keep `github.com/invopop/jsonschema` pinned at `v0.13.0`.
4. Update `go.work` so local development replaces the new pinned version, then verify consumers against the published module from a clean clone or with workspace mode disabled.
5. Merge the consumer-pin commit and dry-run/publish zarlcode and swebench-eval.

For a coordinated zarlcode + swebench-eval release, use separate release-dispatch runs when their versions differ. The workflow accepts one version for every selected module, so `custom_modules=zarlcode,swebench-eval` is only correct when both receive the same version.

## Release verification

- [ ] Real TUI soak: code task, docs-only task, failing-then-passing verification, manual executive compaction, and handover compaction
- [ ] `sweeval` smoke with `--zarlcode-stream-idle` against a scripted/small task
- [ ] release-dispatch dry-run succeeds for each release version/scope
- [ ] At least one Homebrew credential path is configured
- [ ] Published zarlcode archive installs and reports the expected version
- [ ] Homebrew formula updates and installs
- [ ] `zarlcode upgrade status` sees the published release

## Later release planning

- Consider whether beta packages have matured to shared/stable tier.
- Consider adding `zarlai` to the standard CI matrix if CGO dependencies are resolved.
