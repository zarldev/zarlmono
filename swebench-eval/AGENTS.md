# AGENTS.md — `swebench-eval`

SWE-bench evaluation driver in its own module so parquet dependencies do not enter other modules.

```bash
go test -C swebench-eval -count=1 ./...
```

Keep evaluation arms and report labels deterministic. Avoid silently treating unknown configuration or ablation names as baseline because that corrupts comparisons.
