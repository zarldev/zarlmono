# Sub-agents in the timeline: parallel spawned runs, expanded in browse mode (Tab).
# Render from repo root:  vhs zarlcode/docs/images/screen-subagents.tape
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/screen-subagents.gif

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

# Ask zarlcode to clone a real project so sub-agents have packages to map.
Type "clone https://github.com/gohugoio/hugo.git --depth=1 into demo-repo"
Enter
Sleep 8s
Type "cd demo-repo"
Enter
Sleep 3s

# A prompt that fans out to read-only explorer sub-agents.
Type "delegate to sub-agents to map hugolib, commands, and tpl in parallel, then summarize"
Enter
Sleep 28s

# Enter browse mode and expand a collapsed sub-agent item.
Tab
Sleep 1s
Up 1
Sleep 1s
Space
Sleep 2s
Space
Sleep 1s
Escape
Sleep 1s

Ctrl+C
Sleep 1s
Enter
