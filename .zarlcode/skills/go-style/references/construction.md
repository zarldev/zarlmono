# Construction Examples

[go-style](../SKILL.md) owns construction and boundary policy. Load these examples only for parsing, wiring, options, or lifecycle construction work.

## External parsing example

The current `zkit/vectorstore/qdrant` API separates `ParseEndpoint` from `NewClient`:

```go
endpoint, err := qdrant.ParseEndpoint(rawURL)
if err != nil {
    return fmt.Errorf("parse qdrant endpoint: %w", err)
}
client := qdrant.NewClient(endpoint)
```

Inspect the selected module API before using this shape. Parsing untrusted HTTP, environment, flags, config files, storage, messages, or user text establishes semantic values at entry. Merely casting a raw string to a named type does not validate it.

## Runtime transitions are not wiring checks

A parsed amount can still exceed an account's current balance. A valid closer can still be registered after shutdown begins. Those operations check mutable state atomically and can return domain errors. For example, `zkit/zapp.App.AddCloser` reports closed state and duplicate names; that does not justify checking every controlled dependency for nil.

Meaningful absence, optional capabilities, and nil-sensitive external APIs need their own documented semantics. Keep external compatibility handling in the adapter.

## Review smell sweep

For changed Go code, search for:

- dependency nil checks or reflection-based typed-nil helpers in assembly/options;
- sentinel errors named `Err...Required` for controlled wiring;
- constructors changed to return errors solely for impossible nil/value checks;
- options that silently ignore values or depend on setup order;
- runtime clamping/default repair for values already known at composition;
- tests for impossible nil wiring instead of observable domain behavior;
- raw strings/ints/maps passed inward where parsing should establish an invariant;
- constructor goroutines without returned ownership or partial-failure cleanup.

Classify each check as external parsing, trusted assembly, or runtime transition before changing it. Remove redundant wiring checks, not genuine boundary rejection or invariant-preserving state checks.

## Verification

- Test parsers for accepted and rejected external representations.
- Test constructors through useful behavior, not impossible controlled dependencies.
- Test state conflicts and atomic runtime transitions separately from parsing.
- Test stop/wait and partial-failure cleanup for lifecycle owners.
- Run focused external-package tests and configured analysis.

For option API shapes, see [go-zkit](../../go-zkit/SKILL.md); for startup, partial-failure cleanup, and stop/wait behavior, see [go-concurrency](../../go-concurrency/SKILL.md).
