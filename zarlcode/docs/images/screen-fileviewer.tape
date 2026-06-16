# File viewer (Ctrl+F): Files / Skills / Agents / Hooks tabs with nav + preview.
# Render from repo root:  vhs zarlcode/docs/images/screen-fileviewer.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/screen-fileviewer.gif

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

# Ask zarlcode to clone a real project so it has files to browse.
Type "clone https://github.com/gohugoio/hugo.git --depth=1 into demo-repo"
Enter
Sleep 8s
Type "cd demo-repo"
Enter
Sleep 3s

# Now ask the real demo prompt.
Type "what files live under hugolib?"
Enter
Sleep 5s
Escape
Sleep 1s

# Open the file viewer and walk the four tabs.
Ctrl+F
Sleep 2s
Down 3
Sleep 1s
Enter
Sleep 1s
Tab
Sleep 2s
Tab
Sleep 2s
Tab
Sleep 2s
Escape
Sleep 1s

Ctrl+C
Sleep 1s
Enter
