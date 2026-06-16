# Model picker (Ctrl+E): provider tabs + scrollable model list, free-text fallback.
# Render from repo root:  vhs zarlcode/docs/images/screen-modelpicker.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/screen-modelpicker.gif

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

# Open the picker, cycle providers, scroll models.
Ctrl+E
Sleep 2s
Tab
Sleep 2s
Tab
Sleep 2s
Down 4
Sleep 2s
Escape
Sleep 1s

Ctrl+C
Sleep 1s
Enter
