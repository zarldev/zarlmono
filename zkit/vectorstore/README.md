# Vector store

Vector-store integrations live under `zkit/vectorstore/`.

## Current state

- `vectorstore/qdrant` provides the current Qdrant HTTP client used by shared assistant infrastructure.
- There is not yet a broad top-level `vectorstore` Go package or canonical cross-backend interface.

## Status

Beta / evolving. Keep generic contracts small and consumer-driven; add a top-level interface only when at least two real backends or consumers need the same shape.
