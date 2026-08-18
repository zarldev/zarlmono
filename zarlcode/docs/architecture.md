# zarlcode architecture

This document records the extension rules for zarlcode. New behavior should attach to one of these seams before changing the live loop.

## Runtime composition

A turn is assembled from a workspace, run target, instruction snapshot, catalog snapshot, tool registry, guardrail pipeline, prompt stack, and event sink. `LiveRunner.source` is the composition point. The same assembly is used by interactive runs, headless runs, and `Inspect`, so a tool or policy must be registered there rather than in a TUI-only path.

The registry is the runtime capability graph. Built-in tools, MCP tools, computer tools, dynamic tools, and workspace-authored catalog tools all become `tools.Tool` values. Tool names are the stable consumer-facing identifiers; provider tags group replaceable sources. Dynamic providers must not shadow built-ins, and cleanup must unregister the provider's tools when the source is removed.

## Extension seams

| Goal | Seam | Durable or live |
|---|---|---|
| Add a model provider | `engine.BuildProvider` and provider registration | live settings |
| Add a model-facing capability | `tools.Tool` plus registration in `LiveRunner.source` | live per turn |
| Add an external tool source | MCP or `dynamic.Registrar`, tagged with a provider | catalog + live registry |
| Change tool policy | `sourcechain.Pipeline` / guardrail or `engine.modeFilteredSource` | live per turn |
| Add workspace-authored behavior | `catalog` entry, skill, agent, or hook | files + live snapshot |
| Add model-visible context | log it through the runner/session event path, then derive the prompt from that record | durable |
| Add UI | consume runner events and render session state; do not put policy in Bubble Tea handlers | live |
| Add a human command | CLI/TUI command path, without manufacturing a model turn | live |

A capability is complete only when its provider, registration/definition, and consumer are present. For example, an MCP server is not merely a connection dialog: its discovered tools must be registered, governed by the same guardrails, visible to inspection, and removed on disconnect.

## Invariants

- **Model-visible means reconstructable.** Anything included in a model request must come from the prompt assembly inputs or a recorded session/tool event. Do not inject hidden mutable state in a provider or TUI callback.
- **Inspection is authoritative.** `LiveRunner.Inspect` must enumerate the same effective tool roster and prompt sources as the next turn. If a new capability is absent from inspection, it is not finished.
- **Registration is reversible.** A dynamic or external source owns its provider tag and unregisters all of its tools on removal. Registrations must not leak across turns or sessions.
- **Policy wraps capabilities.** Plan/build mode, sandboxing, hooks, and guardrails wrap the shared source. Individual tools must not grow consumer-specific policy forks.
- **Snapshots cross boundaries.** Catalogs, tool lists, prompt inputs, and UI events are copied or immutable snapshots. A reload replaces a snapshot atomically; malformed entries are reported without discarding valid entries.
- **Profiles are composition, not forks.** Settings and explicit run targets should select capability clusters and policies. Do not create a second agent loop for a different surface.

## Composition checklist

Before adding a capability:

1. Name the service/provider and its consumer-side interface, if a seam is required.
2. Decide whether its state is durable session data, workspace configuration, or live turn state.
3. Register it in `LiveRunner.source`; apply the shared policy pipeline.
4. Make it appear in `Inspect` and in the TUI/headless event path where relevant.
5. Define cleanup and collision behavior for external or dynamic registrations.
6. Add focused tests for the observable contract, including reload, cancellation, and snapshot behavior where applicable.

These rules are adapted from DeepSeek Harness's plugin tree, capability seams, reversible effects, profile composition, and logged session model, while retaining zarlcode's Go ownership and consumer-side interface conventions.
