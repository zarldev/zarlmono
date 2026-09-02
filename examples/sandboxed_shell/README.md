# Sandboxed shell

A deterministic Linux example of zkit's kernel-enforced shell confinement.
It applies a narrow Landlock policy to two real subprocesses:

- a write inside the temporary workspace succeeds;
- a write to an ungranted sibling directory is denied and no file is created.

```bash
go run -C examples ./sandboxed_shell
```

On Linux kernels without Landlock, the example reports that it was skipped. On other
platforms, it reports that this mechanism requires Linux. This is the enforcement layer;
command guardrails and human approval remain separate policy layers.
