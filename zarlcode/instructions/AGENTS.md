# AGENTS.md — `zarlcode/instructions`

Owns discovery, indexing, loading, ordering, and budgeting for workspace `AGENTS.md` and `CLAUDE.md` files.

## Contracts

- Root instructions are loaded eagerly; nested instructions are listed first and loaded only when work enters their subtree.
- `Discover` and `ListNested` must share the same traversal exclusions and stable depth/path ordering.
- Ignore generated or noisy trees: VCS metadata, dependencies, build output, `.zarlcode/sessions`, and `.zarlcode/pr-worktrees`.
- An ignored `.zarlcode` subtree is path-specific; do not globally ignore an unrelated directory with the same basename.
- `LoadOne` accepts only workspace-relative paths returned by discovery. Preserve lexical containment and reject absolute or escaping paths.
- Instruction bodies must be valid UTF-8 text. Byte caps truncate at a valid UTF-8 boundary and visibly report omitted content.
- Individual unreadable files are collected as errors without hiding other valid documents.

## Testing

Use temporary directory trees and assert consumer-visible relative paths. Cover both `Discover` and `ListNested` whenever traversal policy changes.

```bash
go test -C zarlcode -count=1 ./instructions
```
