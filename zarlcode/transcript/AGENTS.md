# AGENTS.md — `zarlcode/transcript`

Owns the canonical in-memory conversation timeline, durable record conversion, validation, event projection, and Markdown export.

## Persistence contracts

- Entry kinds, persistence classes, record fields, and stored enum strings are compatibility identifiers. Change them only with an explicit migration or unsupported-format decision.
- `FromRecords` validates untrusted persisted representations and never silently repairs unsupported shapes.
- Rejected transcripts remain unchanged in storage; parsing or resume must not partially rewrite them.
- Preserve every provider event occurrence even when an upstream provider reuses a tool or spawn identifier. Internal entry identity remains unique and ordering remains deterministic.
- Builders update only the intended active occurrence and produce threads that pass `Validate`.
- Canonical transcripts are distinct from compacted provider context. Resume and export use durable canonical history.
- Exported Markdown must redact or omit secrets and unstable internal details according to its public contract.

## Testing

Use records as the storage-boundary fixture. Cover round trips, malformed JSON, trailing data, unknown enums, duplicate provider identifiers, ordering, and preservation after rejection.

```bash
go test -C zarlcode -count=1 ./transcript
```
