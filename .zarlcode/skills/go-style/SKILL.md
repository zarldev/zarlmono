---
name: go-style
description: Authoritative zarlmono Go policy and topic index. Load for substantive Go implementation, review, or refactor; design and construction examples are on-demand references.
---

# Go Style and Policy

This is the single normative Go skill for zarlmono, after explicit user requests and repository-local instructions. Other Go skills are detailed references and may not override it. Load the owning nested `AGENTS.md`; module/package requirements remain in force.

## North star

Use Go's semantics to enforce the domain's legal states, transitions, ownership, atomicity, errors, and lifecycle. Do not start from a named pattern.

An abstraction must enforce an invariant, establish ownership or lifecycle, express domain behavior, or enable required substitution. Otherwise remove it. Do not introduce builders, fluent mutation, getter/setter ceremony, generic application repositories, service locators, or framework layers without a concrete invariant.

Distinguish applications consuming zkit from development of zkit itself. Reusable generic collections, stores, buses, clients, options, and capability interfaces are legitimate library APIs when they express a real shared contract. Applications expose their own domain operations rather than introducing generic application-wide repositories. Do not force library primitives into an invented application domain or change existing public contracts merely to conform to an example here.

## Required workflow

1. Inspect repository instructions, `go.mod`/`go.work`, active toolchain, generated code, tests, conventions, and commands. Use the topic routing below to load only relevant detail.
2. Define legal states and transitions, absence semantics, ownership and aliasing, atomicity, substitutions, caller-visible errors, lifecycle, and final logging boundary.
3. Implement the smallest direct-Go solution and preserve unrelated user work.
4. Format and run focused tests; run generation, race tests, vet, and configured analysis when implicated. `./...` does not cross this repository's module boundaries; use `go test -C <module> ...` or root Task targets.
5. Review the final diff using the required sweep below. Report changes, verification, and blockers; do not imply unrun checks passed.

Use version-specific APIs such as `t.Context`, `testing/synctest`, `sync.WaitGroup.Go`, and newer `errors` helpers only when supported by the active repository toolchain.

## Types, ownership, and mutation

- Use named types for units, parsing, vocabulary, domain meaning, or non-interchangeability. Use aliases only when interchangeability is intentional.
- Pass, return, store, and send domain data as values by default.
- A pointer requires meaningful absence, identity, shared mutation, retained reference, lifecycle or non-copyable state, an external API, or measured copy-cost semantics.
- Review every pointer collection and channel element.
- Export a field when assigning any value of its type preserves validity. Mutate owned valid state directly.
- Use methods for real transitions, coordinated fields, or enforced invariants—not getters, setters, fluent copies, or builders.
- Slice and map copies may alias mutable storage. Clone when the contract requires independence and callers retain mutable aliases; shallow clones do not isolate nested mutable fields. Actual ownership transfer can avoid copying only when the previous owner relinquishes all mutable aliases. Document borrowing, transfer, or snapshot semantics explicitly.
- Never copy used mutexes, atomics, `sync.Once`, goroutine owners, or documented no-copy values.

## Construction, parsing, and transitions

These are different boundaries, not interchangeable meanings of validation:

| Boundary | Responsibility |
|---|---|
| External representation | Parse and validate raw HTTP/config/storage/message/user input into semantic types; return meaningful input errors. A cast to a named type alone does not establish validity. |
| Trusted construction | Assemble already-created dependencies and known-valid semantic values from controlled composition; do not revalidate wiring. |
| Runtime domain transition | Check current state, authorization, conflicts, capacity, and coordinated invariants atomically at the owner; return meaningful domain errors even when the input is well typed. |

- Composition roots parse configuration, select implementations, and open resources. Pass known-valid dependencies and semantic values directly inward.
- Constructors return concrete usable types by default. A direct assembly constructor does not need an error return. Errors belong to genuine fallible operations appropriate to that boundary, such as opening a resource, not impossible internal wiring.
- Required dependencies are positional when no truthful local default exists.
- Do not add dependency nil checks, typed-nil reflection, silent guards, deep normalization/repair, or impossible error returns for creation and flow the repository controls. Reason about the call graph instead. Meaningful absence, optional capabilities, genuine external library boundaries, and runtime state checks are different contracts, not redundant wiring checks.
- Use ordinary parameters, a useful zero value, or a small configuration value when sufficient. Typed functional options are not mandatory ceremony. When functional options are warranted, use the repository's `zkit/options.Option[T]`, targeting an unexported config or the concrete type as appropriate to the existing API.
- Options assign optional policy directly. They do not perform I/O, start goroutines, parse raw config, revalidate controlled dependencies, silently ignore values, replace required dependencies, or require order-dependent setup. Do not add no-op option guards.
- Return pointers for mutable lifecycle, identity, retained or non-copyable state, or pointer method sets.
- A truthful domain memory implementation may be a default. Never use a no-op or memory default that violates required durability, delivery, authorization, or audit semantics.
- Prefer explicit `Run`/`Start` for work. A constructor may start goroutines only when the returned object is their explicit lifecycle owner with a reliable stop and wait path, error delivery, and documented ownership. Any failure before returning that owner must stop and wait for already-started work and release acquired resources. Options never start work.

See [construction reference](references/construction.md) for examples and a focused review checklist, not a second policy authority.

## Boundaries and absence

- Keep domain, transport, config, generated, and persistence structs separate where they represent different contracts.
- Distinguish omission, storage NULL, invalid zero, domain absence, and repository not-found.
- Prefer `(T, error)` with semantic not-found identity for domain repository lookup over `(T, bool, error)` or zero `T` plus nil error. Library capabilities may legitimately use boolean results, such as `LoadOrStore`; preserve their stated contract.
- The config boundary owns environment, flag, and file parsing and returns typed values. Runtime transition checks remain necessary; parsing once does not freeze mutable state.

## Interfaces and packages

- Define minimal interfaces in consuming packages, containing only methods used there. A reusable zkit package is also a consumer of capabilities supplied by callers.
- Add interfaces for required substitution or external capabilities, not hypothetical testability.
- Constructors return concrete types unless a domain-specific implementation selector or established library contract requires an interface.
- Assert conformance with the correct method set where both types can be referenced without reversing dependencies. Beside the implementation is useful when acyclic; otherwise use the consumer/composition package or an external integration test. Never import an application into a lower-level zkit package just to assert conformance.
- Keep domain implementations flat until a real dependency, cohesion, or generated-code boundary justifies a package split.
- Do not create generic application-wide storage factories.

## Enums

- This repository uses `github.com/zarldev/goenums` for closed vocabularies; follow root and nested generator requirements. Edit source `*_enum.go`/`enums.go`, run the owning generation command, and never edit `*_enums.go`. This is repository tooling, not a universal requirement imposed on unrelated Go libraries.
- Reserve invalid zero unless a natural valid zero is deliberate.
- Use generated containers, accessors, matchers, and completeness checks where available.
- When there is real configured implementation selection, use the generated implementation enum consistently for typed config, runtime selection, and contract cases. A single implementation does not need a selector/factory merely for ceremony.

## Data boundaries

Database contracts, memory equivalence, transaction scope, adapter representations, and adopted query tooling belong to [go-data](../go-data/SKILL.md); SQLC configuration and generation are its on-demand reference. Do not impose a database tool on unrelated adapters without a repository decision.

## Application lifecycle and concurrency

The application is the composition root, not a service locator. The opener owns the closer unless ownership is explicitly transferred. Register cleanup immediately, stop and wait for dependents before closing dependencies, continue appropriate cleanup after failures, and use bounded shutdown contexts. Reverse registration order is useful only when it matches dependency order; inspect a lifecycle helper's actual timeout behavior.

Every goroutine needs an owner, stop condition, wait path, error path, data ownership, and shutdown order. Detailed context, channel, synchronization, and draining guidance belongs to [go-concurrency](../go-concurrency/SKILL.md).

## Errors and logging

Errors are consumer-visible values; do not panic for ordinary parsing, configuration, I/O, dependency, or domain failures. [go-errors](../go-errors/SKILL.md) owns semantic identities, diagnostic cause exposure, wrapping, cancellation mapping, and single-point boundary logging.

## HTTP and infrastructure

- Preserve standard `net/http` compatibility at boundaries.
- Handlers parse, validate, invoke narrow capabilities, map outcomes, and log consumed unexpected failures once. They do not own business or storage logic.
- Prefer the standard library first. For maintained shared policy, inspect the current `github.com/zarldev/zarlmono/zkit/...` package and its real API before use; see [go-zkit](../go-zkit/SKILL.md).
- Reuse and bound HTTP clients and servers; propagate contexts and close response bodies.
- Introduce external infrastructure only for a named durability, coordination, scale, interoperability, or operational requirement, with selection, contracts, and lifecycle ownership together.

## Testing

[go-testing](../go-testing/SKILL.md) owns external-package test requirements, implementation selection, contract coverage, fixtures, deterministic time, and race verification. Test observable behavior and runtime conflicts, not impossible controlled wiring.

## Required review sweep

Search changed code for:

- pointer collections and escaped mutable aliases;
- builders, fluent methods, getters, and setters;
- interfaces without a required consumer substitution;
- missing/incorrect conformance assertions or assertions forcing cyclic imports;
- raw config switches and untyped implementation selectors;
- transport/storage/generated types leaking into domain packages;
- ambiguous absence returns;
- logging below consuming boundaries and string error comparisons;
- redundant trusted-wiring checks and hidden/unowned constructor goroutines;
- hand-written fixed-shape SQL bindings in SQLC-owned adapters;
- unowned resources or dependency-unsafe shutdown order;
- arbitrary sleeps, non-isolated parallel tests, and test frameworks.

For each exception, state the concrete invariant, ownership, lifecycle, external API, or measured constraint that requires it. This policy guides scoped work; it does not authorize unrelated API rewrites.

## Topic routing

Load this skill for substantive Go work. Load focused skills only for the topic being changed; open subordinate reference documents only when their examples or mechanics are needed. This set is self-contained and requires no external workflow skill.

| Topic | Owner or on-demand reference |
|---|---|
| Type shapes, alias ownership, interfaces, conformance, naming | [Design examples](references/design.md) |
| Parsing versus assembly, options, runtime checks | [Construction examples](references/construction.md) |
| Errors, diagnostic causes, translation, logging | [go-errors](../go-errors/SKILL.md) |
| Goroutines, channels, synchronization, cancellation, shutdown | [go-concurrency](../go-concurrency/SKILL.md) |
| Public behavior, tables, fixtures, contracts, race tests | [go-testing](../go-testing/SKILL.md) |
| Repositories, migrations, databases, transactions | [go-data](../go-data/SKILL.md) |
| Adopted SQLC configuration, queries, generation | [SQLC reference](../go-data/references/sqlc.md) |
| Protobuf, Buf, handlers, streaming | [go-connectrpc](../go-connectrpc/SKILL.md) |
| Building reusable zkit or consuming its current APIs | [go-zkit](../go-zkit/SKILL.md) |
