---
description: Review current changes like the zarlcode reviewer profile
argument-hint: "[focus]"
---
Review the current workspace changes. Focus: ${ARGUMENTS:-correctness, lifecycle, compatibility, security, and repository conventions}.

Use read-only inspection unless I explicitly ask for fixes. Check:
- `git status --short` and relevant diffs.
- Applicable `AGENTS.md` files for touched paths.
- Go invariants from the root `AGENTS.md` when Go code is involved.

Return findings ordered by severity with file:line references where possible. Keep praise and summaries short.
