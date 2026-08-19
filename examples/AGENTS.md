# AGENTS.md — `examples`

Runnable harness examples, many with a `-scripted` mode that avoids live provider calls.

```bash
go test -C examples -count=1 ./...
```

Keep examples deterministic in scripted mode and avoid introducing dependencies on live credentials into ordinary tests.
