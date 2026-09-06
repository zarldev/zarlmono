# Go Design Examples

[go-style](../SKILL.md) owns type, ownership, interface, construction, and enum policy. This reference adds examples and naming detail; it is not a second policy authority.

## Type choices in practice

- `type Milliseconds = int64` intentionally preserves interchangeability; `type AccountID string` separates identities and permits domain methods/parsing. Choose semantics, not a fixed alias/defined-type ratio.
- A defined type alone is not proof of validation: arbitrary conversions and exported fields may still create invalid values. See the core boundary table.
- A pointer receiver can be required for mutation or consistent method sets even when nil is not meaningful.
- `maps.Clone` and `slices.Clone` are shallow. State whether a mutable input is borrowed, copied, or transferred; passing a descriptor alone does not relinquish aliases. Clone nested mutable fields for independent snapshots.
- `zkit/zsync.Map` synchronizes entries, not nested values or compound transitions. Its snapshot copy is not automatic deep isolation.
- Keep unrelated JSON/YAML/protobuf/SQLC/database tags out of domain structs; a low-level wire or storage library may legitimately own these representations.
- Keep exported fields before private state when it improves readability. Embed for a real semantic relationship; otherwise use named composition.

## Consumer capabilities and conformance

```go
type UserFinder interface {
    User(context.Context, UserID) (User, error)
}

var _ UserFinder = (*PostgresUsers)(nil)
var _ Scope = AnyHostScope{} // Deliberate value method set.
```

Small duplicate interfaces in separate consumers can be valid: structural typing avoids coupling them. Prefer one-method capabilities and compose them only when the consumer needs the combined contract. A reusable zkit package can itself consume capabilities supplied by callers.

Place assertions beside implementations when acyclic; otherwise use consumer/composition wiring or an external integration test importing both types. Normal typed wiring also proves conformance. Do not relocate a consumer interface into a lower-level package just to accommodate an assertion.

Avoid `IThing`, `ThingInterface`, broad managers, and `any` catch-alls. Preserve legitimate generic library capabilities rather than inventing application wrappers.

## Scope-based names

Name length should match the scope in which the reader must retain its meaning.

```go
for i := range items {}
for k, v := range values {}
ctx := r.Context()
requestID := uuid.New()
connectionTimeout := 30 * time.Second
```

Use one-letter receivers matching the type; two letters are fine for established abbreviations such as `tx` and `rw`.

## APIs

- No `GetX` or `SetX` for plain field access.
- Name methods for behavior, transitions, computation, validation, or side effects.
- Constructors use `NewType`; predicates use `Is`, `Has`, or `Can` when those words clarify the result.
- Avoid redundant `Do`, `Perform`, `Manager`, `Interface`, and package-name stuttering.
- Generic collection operations such as `zsync.Map.Get` and `Set` are real library behavior, not forbidden domain getter/setter ceremony. Preserve established public API names unless a scoped change requires otherwise.

## Errors

Caller-visible sentinel errors use exported `ErrXxx` names. Keep the error vocabulary small and semantic.

## Constants and enums

Use ordinary Go casing for simple constants. For closed vocabularies, follow the generated enum policy in [go-style](../SKILL.md), not an uppercase `iota` convention.

## Packages and files

- Packages are short, singular, lowercase, and domain- or capability-specific.
- Avoid `util`, `common`, `helpers`, and names that repeat their parent context.
- Handwritten Go files use descriptive snake_case names.
- Preserve generator-defined filenames for generated files.
