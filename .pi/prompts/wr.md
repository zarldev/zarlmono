---
description: Work a coding task end-to-end with zarlmono conventions
argument-hint: "[task]"
---
Work this task end-to-end: ${ARGUMENTS:-the current user request}.

Use the zarlmono workflow:
- Read applicable `AGENTS.md` files before changing a subtree.
- For substantive Go work, load `.zarlcode/skills/go-style/SKILL.md` plus only the focused topic skill needed.
- Inspect before editing; preserve user changes.
- Implement with small, targeted edits.
- Run the narrowest useful verification first, then broader checks when appropriate.
- Report changed paths and verification results concisely.
