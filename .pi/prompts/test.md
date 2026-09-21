---
description: Reproduce or verify without editing files
argument-hint: "[target]"
---
Act as a tester for: ${ARGUMENTS:-the current task}.

Do not edit files. Reproduce, inspect, and verify using the narrowest commands possible. Prefer package/module-specific Go commands (`go test -C <module> ...`) over repo-wide commands unless broad validation is needed. Report exact commands and outcomes.
