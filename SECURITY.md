# Security Policy

## Supported versions

Security fixes are developed against `main` and released in the next appropriate
module patch release. This project does not currently maintain long-term-support
release branches; users should reproduce reports against the latest release when
possible.

## Reporting a vulnerability

Report vulnerabilities through [GitHub private vulnerability reporting](https://github.com/zarldev/zarlmono/security/advisories/new).
Do not open a public issue or discussion for an undisclosed vulnerability.

Include the affected module and version, impact, reproduction steps or a proof of
concept, and any known mitigations. Please avoid including live credentials,
private repository contents, or other third-party data.

A maintainer will acknowledge the report, validate its scope, coordinate a fix and
release, and agree on disclosure timing with the reporter. Response times are
best-effort; there is currently no formal service-level agreement.

## Security boundary

This repository intentionally includes tools that execute processes, mutate files,
access networks and browsers, connect to MCP servers, and send selected context to
LLM providers. Those documented capabilities are not vulnerabilities by themselves.
Reports are especially useful when they demonstrate an unexpected boundary bypass,
credential disclosure, sandbox or workspace escape, unsafe update behavior, or a
way for untrusted content to gain capabilities beyond the configured policy.
