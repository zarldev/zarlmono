# Cockpit / expanded dashboard (Ctrl+L): context gauge, sparklines, per-turn history.
# Render from repo root:  vhs zarlcode/docs/images/screen-cockpit.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/screen-cockpit.gif
Output zarlcode/docs/images/screen-cockpit.mp4

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

# Run a turn so the cockpit has real token/cost/throughput data.
Type "fetch https://pkg.go.dev and tell me what it is"
Enter
Sleep 12s

# Expand the sidebar cockpit into the full-screen dashboard.
Ctrl+L
Sleep 3s
Down 4
Sleep 2s
PageDown
Sleep 2s
Escape
Sleep 1s

Ctrl+C
Sleep 1s
Enter
