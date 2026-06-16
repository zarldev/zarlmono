# Plan <-> Build mode (Shift+Tab): read-only plan-review surface and UI tint shift.
# Render from repo root:  vhs zarlcode/docs/images/screen-planmode.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/screen-planmode.gif

Require bash
Require go

Set Shell bash
Set FontSize 12
Set Width 1440
Set Height 1024
Set Theme "Catppuccin Mocha"

Type "zarlcode"
Enter
Sleep 3s

Type "hello"
Enter
Sleep 4s
Escape
Sleep 1s

# Toggle into plan mode, ask for a plan, watch it build, toggle back.
Shift+Tab
Sleep 2s
Type "refactor the auth package to thread context everywhere"
Enter
Sleep 15s
Sleep 2s
Shift+Tab
Sleep 2s

Ctrl+C
Sleep 1s
Enter
