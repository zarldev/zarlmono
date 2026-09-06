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

Use `go tool task tools-test` for all root-tool regression tests. The standard
`go tool task check` additionally verifies every child module with `GOWORK=off`,
then builds, vets, and tests the current workspace graph. `go tool task lint` covers
the root tooling module and all child modules; `go tool task release-check` adds the
zkit race suite, production site build/audit, and exact-toolchain isolated zkit
compilation. Release workflows separately build, vet, and test selected release
modules outside `go.work` before tags or artifacts are published.

The terminal smoke always creates disposable HOME/XDG state and an isolated tmux
socket. It never accepts a real user profile, uses fake credentials, and does not
make a model completion request. Persistence is verified through restart and unlock
behavior; storage schema and encryption-format details remain in owning package
tests. Cancellation tears down the tmux server and temporary files.

Use `go tool task tui-smoke -- -binary /absolute/path/to/zarlcode -timeout 45s`
to select an existing build and a per-transition timeout. The default builds the
application and allows 30 seconds per transition. Use `-root` with the direct Go
command to select another checkout. No workflow is embedded in a shell wrapper.
