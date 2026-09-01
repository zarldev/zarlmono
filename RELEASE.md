# Release Checklist

Use this checklist before each release.

## Pre-release

- [ ] `go tool task release-check` — build + vet + tests + strict lint + zkit race suite pass
- [ ] `git status --short` is empty; push the release commit and run from the current `origin/main` tip
- [ ] CHANGELOG entry exists for every module being released
- [ ] `go tool task tui-smoke` — bounded real-terminal startup, resize, Help, quit, and shutdown pass (requires `tmux`)
- [ ] Credentialed terminal soak covers streaming, tools, and cancellation when the release changes those paths

## Recommended module order

The canonical path is the GitHub Actions **release-dispatch** workflow. For a coordinated `zkit` + `zarlcode` release:

1. Dispatch `zkit` alone in `dry-run`, then `publish` mode.
2. Wait until the public proxy resolves it:
   `GOPROXY=https://proxy.golang.org go list -m github.com/zarldev/zarlmono/zkit@vX.Y.Z`
3. Pin `zarlcode/go.mod` to the published zkit version, run `go mod tidy`, verify with `GOWORK=off`, commit, and push.
4. Dispatch `zarlcode` alone in `dry-run`, then `publish` mode.

The workflow rejects a scope that combines `zkit` with consumers because the local `go.work` can hide unpublished dependency pins.

## Release dispatch

Inputs:

- `version`: canonical semver such as `v0.16.0`
- `scope`: direct choices for `zkit`, `zarlcode`, `swebench-eval`, `examples`, plus `custom` for consumer-only combinations
- `custom_modules`: comma-separated consumer modules for `scope=custom`; `zkit` must be released alone
- `mode`: `dry-run` to validate and print the plan, or `publish` to tag and push

Every selected module must have exactly one dated `## [module/vX.Y.Z] — YYYY-MM-DD` heading in `CHANGELOG.md`.

Always run `dry-run` first. The workflow verifies the `origin/main` tip, absent local and remote tags, dated changelog headings, all published internal dependency pins, module-isolated dependency integrity, workspace build/vet/test/lint, the zkit isolated race/exact-Go suite, and the locked documentation install/build/audit. Consumer source is validated through `go.work` until its `go.mod` can pin the newly published sibling module. Publish mode creates annotated tags, pushes the selected set in one command, dispatches exactly one zarlcode publisher when applicable, and waits for it.

## Go versions

The root workspace and all published modules require Go 1.27.0. CI, releases, and `release-check` build with Go 1.27.0; the release gate also performs a module-isolated zkit compile with that exact toolchain so the declared minimum cannot drift from automation.

## Manual fallback

If automation is unavailable:

1. Create annotated module tags:
   `git tag -a <module>/vX.Y.Z -m "<module> vX.Y.Z"`
2. Push them only after completing the same isolated gate:
   `GOWORK=off go test -C <module> -count=1 ./...`
   `git push origin <module>/vX.Y.Z`
3. If an existing valid zarlcode tag failed only during publication, dispatch `release.yml` manually with that exact tag. It refuses arbitrary branches and lightweight tags.

For a coordinated release, follow the same zkit-first/pin-consumer order above.

## Post-release

- [ ] Verify tag objects are annotated: `git cat-file -t <module>/vX.Y.Z` prints `tag`
- [ ] Annotated tags are not cryptographically signed by default; if signing is required, verify the tag signature with `git tag -v <module>/vX.Y.Z`
- [ ] For zarlcode, confirm the release has exactly four archives plus `checksums.txt`; each archive contains one root-level `zarlcode` executable, and checksums and embedded `-version` match
- [ ] Verify keyless artifact provenance: `gh attestation verify <archive> --repo zarldev/zarlmono`
- [ ] Confirm the Homebrew tap formula reached the released version; Homebrew publication is a required publisher step
- [ ] Run `zarlcode upgrade status` or `zarlcode upgrade --dry-run --no-restart`
- [ ] Verify the installed binary reports the released version

Prefer a new patch release when tagged source is wrong. Moving or deleting a published tag/release requires explicit operator approval. Homebrew failures do not require moving the source tag: repair credentials and rerun the publisher for the existing valid tag.

The bounded `tui-smoke` task covers terminal lifecycle without submitting a prompt. Credentialed provider/tool streaming and a real `brew install` remain operator checks because CI lacks external credentials/services and may not have Homebrew. Record them explicitly rather than treating them as automated gates.

## Release authenticity

The workflow intentionally creates annotated Git tags without storing a long-lived signing key in repository secrets. The repository's active `release-tags` ruleset protects `zkit/v*`, `zarlcode/v*`, `swebench-eval/v*`, and `examples/v*` from update/deletion. Keep tag creation limited to trusted release operators and automation through repository permissions. The publisher emits GitHub OIDC-backed build-provenance attestations for every zarlcode archive and `checksums.txt`; consumers should verify those attestations rather than treating an annotated tag as a cryptographic signature.

If policy requires a GitHub “Verified” tag object, provision a managed GPG/SSH signing identity or external signing service. Do not add an unmanaged private key solely to make CI tags appear signed.
