# Workflow demo: Plan → model switch → Build → working set → headless follow-up.
# Render from repo root:  vhs zarlcode/docs/images/workflow-demo.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
# NOTE: this records in a temp copy of the small fixture at `workflow-demo-fixture/`.
Output zarlcode/docs/images/workflow-demo.gif

Require bash
Require go

Set Shell bash
Set FontSize 12
Set Width 1440
Set Height 1024
Set Theme "Catppuccin Mocha"

# Prepare a tiny disposable repo off-screen so the recording starts with zarlcode,
# not a wall of setup commands.
Hide
Type "rm -rf /tmp/zarlcode-workflow-demo && cp -R zarlcode/docs/images/workflow-demo-fixture /tmp/zarlcode-workflow-demo && cd /tmp/zarlcode-workflow-demo && git init -q && git add . && git -c user.name='zarlcode demo' -c user.email='demo@example.com' commit -qm init"
Enter
Wait
Show
Sleep 1s
Type "zarlcode"
Enter
Sleep 1s
Enter
Sleep 3s

# Plan first: ask for a read-only design before any mutation.
Shift+Tab
Wait+Screen /shift\+tab build/
Sleep 1s
Type "Plan a tiny change: make Message return a friendlier greeting, then tell me the exact files and tests you would touch. Do not edit yet."
Enter
Sleep 25s
Wait+Screen /shift\+tab build/

# Show model/provider switching as part of the same workflow.
Ctrl+E
Sleep 2s
Tab
Sleep 1s
Tab
Sleep 1s
Down 3
Sleep 1s
Escape
Sleep 1s

# Make sure the model picker is closed and the composer is still in PLAN.
Escape
Sleep 1s
Wait+Screen /shift\+tab build/

# Build: toggle back to BUILD, then submit the implementation prompt.
Shift+Tab
Wait+Screen /shift\+tab plan mode/
Sleep 1s
Type "Build that change now and run go test ./..."
Enter
Sleep 20s

# Inspect the working set and diff.
Ctrl+W
Sleep 2s
Enter
Sleep 3s
Escape
Sleep 1s
Escape
Sleep 1s

# Quit the TUI, then show the same repository can be driven headlessly.
Ctrl+C
Sleep 1s
Enter
Sleep 2s
Type "zarlcode --headless --max-iter 8 --prompt-text 'Run git diff --stat, git diff -- greet.go greet_test.go, and go test ./...; summarize only the repository diff and test result in one short paragraph. Do not edit files.'"
Enter
Wait
Sleep 2s
