# Contributing

Thanks for your interest. A few things to read before opening a PR.

## Workflow

- Branch off `main`. Branches named `feat/...`, `fix/...`, `chore/...`,
  `docs/...` per the existing convention.
- Direct commits to `main` are blocked locally; CI also runs against PRs.
- Keep PRs focused — one logical change per PR is easier to review.
- Verify locally before pushing:

  ```bash
  task test                 # go test -race -count=1 ./...
  cd frontend && npx tsc -b # frontend type check
  task build                # final integrated build
  ```

## Coding style

Go style is documented in [`AGENTS.md`](./AGENTS.md) and the repo-root
[`AGENTS.md`](../AGENTS.md) ("zstyle"). The short version:

- Errors tell a story — wrap with `fmt.Errorf("context: %w", err)` at
  every failure point, log once at boundaries, no `"failed to"` /
  `"unable to"` / `"could not"` prefixes
- Scope-based naming — small scope = short names (`i`, `r`, `w`),
  larger scope = explicit names (`requestID`, `userCount`)
- Small, emergent interfaces — define on the consumer side, not
  design-first
- No setters — variadic functional options instead
- No fire-and-forget goroutines — every goroutine needs lifecycle
  management
- Prefer fakes over mocks; tests use `package_test` (black-box) and
  `t.Run` for table-driven cases
- Each layer owns its types; map at boundaries; no shared "domain"
  package

Frontend: React 19, Tailwind v4, ConnectRPC web client. Keep
abstractions light; favour readability over premature reuse.

## Protobuf

Proto is the source of truth for the API. After editing
`proto/zarl/v1/*.proto`:

```bash
task proto                # buf lint + generate (Go + TS)
```

Don't hand-edit anything under `transport/grpc/gen/` or
`frontend/src/gen/` — it's regenerated.

## Database changes

Schema migrations go in `migrations/`. SQLC queries in
`repository/queries/`. After either changes:

```bash
cd repository && sqlc generate
```

## Tests

- Use `t.Context()` instead of `context.Background()` in tests
- Prefer integration-style tests with the real Dolt / Qdrant when the
  behaviour-under-test is data-shape-sensitive — there are existing
  fakes for higher-level orchestration tests

## Commits

- Imperative subject ≤ 70 chars (`fix:`, `feat:`, `chore:`, `docs:`,
  `refactor:`)
- Body explains the *why* if it isn't obvious from the diff

## License

By contributing you agree that your changes are licensed under the
project's [MIT license](./LICENSE).
