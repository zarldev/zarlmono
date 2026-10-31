# Repository tools

Root tools enforce repository, documentation, release, and terminal-product
contracts. Application behavior and dependency compatibility stay with their owning
packages and module manifests.

| Tool | Focused command | Contract |
| --- | --- | --- |
| `releasecheck` | `go run ./tools/releasecheck/cmd/releasecheck ...` | Validate release scope, versions, changelog entries, internal dependency ordering, and published consumer pins. |
| `genformula` | `go run ./tools/genformula/cmd/genformula ...` | Render the Homebrew formula deterministically from release checksums. |
| `doccheck` | `go tool task doccheck` | Keep canonical quickstart sources synchronized and resolve site-internal links. |
| `repohealth` | `go tool task repohealth` | Validate repository-local skills, agents, instruction metadata, routing, and links. |
| `testpolicy` | `go tool task test-policy` | Enforce external test packages, forbid `*_internal_test.go`, and check changed test-context usage. Set `TEST_POLICY_BASE` to compare with another revision. |
| `tuismoke` | `go tool task tui-smoke` | Exercise real terminal onboarding, credential save, restart/unlock, resize/help, startup cancellation, quit, and shutdown. Requires `tmux`. |
| `coderisk` | `go tool task code-risk` | Report-only function complexity, statement coverage, and CRAP hotspots across the Go modules; no numerical gate. |

Use `go tool task tools-test` for all root-tool regression tests. The standard
`go tool task check` additionally verifies every child module with `GOWORK=off`,
then builds, vets, and tests the current workspace graph. `go tool task lint` covers
the root tooling module and all child modules; `go tool task release-check` adds the
shared-library and application race suites, production site build/audit, and
exact-toolchain isolated zkit compilation. Release workflows separately build,
vet, and test selected release modules outside `go.work` before tags or artifacts
are published.

The terminal smoke always creates disposable HOME/XDG state and an isolated tmux
socket. It never accepts a real user profile, uses fake credentials, and does not
make a model completion request. Persistence is verified through restart and unlock
behavior; storage schema and encryption-format details remain in owning package
tests. Cancellation tears down the tmux server and temporary files.

Use `go tool task tui-smoke -- -binary /absolute/path/to/zarlcode -timeout 45s`
to select an existing build and a per-transition timeout. The default builds the
application and allows 30 seconds per transition. Use `-root` with the direct Go
command to select another checkout. No workflow is embedded in a shell wrapper.

## Change-risk baseline

Run `go tool task code-risk` to execute uncached coverage tests in the root tooling
module (`./tools/...`) and all four application/library/example modules. This can
take several minutes. It uses the current Go build environment and a 10-minute
per-package test timeout. Each run creates an isolated, ignored directory under
`coverage/coderisk/run.*` containing package inventories, atomic coverage profiles,
test logs, environment metadata, `report.md` (top 30 per section), and `report.json`
(all named functions). A test or collection error stops the task without producing
a new report; earlier run directories remain historical results, not a new baseline.

Use `go tool task code-risk -- -top 50` to expand the summary. To regenerate without
rerunning tests, use the completed directory printed by the task:

```bash
go run ./tools/coderisk/cmd/coderisk -input coverage/coderisk/run.XXXXXX -top 50
```

Regeneration requires the same checkout paths and unchanged sources. Collect a
fresh baseline after code or build-configuration changes; reports describe the
working tree, including uncommitted changes, not just HEAD. No CI gate or hosted
service is added, and existing `cyclop` thresholds are unchanged.

### Interpretation

- Ranked per named function/method using `CC² × (1 − coverage_fraction)³ + CC`.
  Complexity comes from the pinned `gocyclo` dependency: base 1 plus `if`,
  `for`/`range`, non-default switch/select cases, `&&`, and `||`.
- Coverage is weighted by executable statements, not coverage blocks or lines.
  Tests instrument each package itself, not dependencies via `-coverpkg`.
- Generated files (standard Go generated marker), test functions, bodyless
  declarations, and files outside the active build are excluded. Nested closures
  contribute to their enclosing named function for both metrics; package-level
  function literals are not scored separately.
- Missing coverage and functions without executable statements have **null**
  coverage/CRAP in JSON and a separate unmeasured section, never an invented zero
  or a low-risk ranking. Check this section alongside the measured hotspots.
- Statement coverage cannot prove branch, cancellation, retry, or concurrent
  interleaving coverage. Scores are investigation priorities, not correctness
  judgments. Do not mechanically split functions just to lower a score.

Start with the highest-risk functions in frequently changed or consequential
paths. Add behavioral tests or clarify responsibilities where useful; establish a
baseline before considering any regression threshold. Compare like-for-like
platforms, build flags, and instrumentation, not repository-wide score totals.
