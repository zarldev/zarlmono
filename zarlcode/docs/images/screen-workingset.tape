# Working set (Ctrl+W): files/turns the agent mutated -> diff browser -> rollback.
# NOTE: this records a real file edit. Run it in a throwaway workspace, not the repo.
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
# Render:  cd /path/to/scratch && vhs /abs/path/screen-workingset.tape
Output zarlcode/docs/images/screen-workingset.gif

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

# Ask zarlcode to clone a real project so it has a file to edit.
Type "clone https://github.com/gohugoio/hugo.git --depth=1 into demo-repo"
Enter
Sleep 8s
Type "cd demo-repo"
Enter
Sleep 3s

# A small, contained edit so the working set has something to show.
Type "add a one-line top-of-file comment to README.md describing this folder"
Enter
Sleep 20s

# Inspect what changed, open the diff, then back out.
Ctrl+W
Sleep 2s
Down 1
Sleep 1s
Enter
Sleep 3s
Escape
Sleep 1s
Escape
Sleep 1s

Ctrl+C
Sleep 1s
Enter
