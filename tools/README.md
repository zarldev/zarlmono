# Repository tools

Repository checks and terminal automation are Go programs with external-package
regression tests. Run them from the repository root:

| Task | Go entry point | Purpose |
| --- | --- | --- |
| `go tool task dependency-pair` | `go run ./tools/dependencycheck/cmd/dependencycheck` | Verify the validated Anthropic/JSON Schema pair independently in each application module. |
| `go tool task test-policy` | `go run ./tools/testpolicy/cmd/testpolicy HEAD` | Check changed tests and the owned tree. Task defaults to `HEAD`; set `TEST_POLICY_BASE` for another revision. |
| `go tool task tui-smoke` | `go run ./tools/tuismoke/cmd/tuismoke` | Exercise real terminal onboarding, encrypted credential save, restart, unlock, resize, help, and shutdown. Requires `tmux`. |
| `go tool task tools-test` | `go test -count=1 ./tools/...` | Run repository tool regression tests, including existing documentation and release tooling. |

The terminal tool always creates disposable HOME/XDG state and an isolated tmux
socket. It never accepts a real user profile. It uses fake credentials and does not
make a model completion request. Cancellation tears down its server and temporary
files. SQLite verification is performed directly in Go; no Python or Bash is needed.

Use `go tool task tui-smoke -- -binary /absolute/path/to/zarlcode -timeout 45s`
to select an existing build and a per-transition timeout. The default builds the
application and allows 30 seconds per transition. Use `-root` with the direct Go
command to select another checkout. No workflow is embedded in a shell wrapper.
