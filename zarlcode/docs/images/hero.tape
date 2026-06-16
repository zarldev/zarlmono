# hero demo: launch zarlcode, run two web_fetch prompts.
# Timing: bump Sleep values 1.5x–2x if your model/connection is slow.
Output zarlcode/docs/images/hero.gif
Require go
Set Shell bash
Set FontSize 12
Set Width 1440
Set Height 1024
Set Theme "Catppuccin Mocha"
Type zarlcode 
Enter 1
Sleep 3s
Type "fetch https://go.dev and summarize what Go is in one sentence"
Enter 1
Sleep 10s
Ctrl+T
Sleep 1s
Up 1
Sleep 1s
Up 1
Sleep 1s
Up 1
Sleep 1s
Up 1
Sleep 1s
Enter 1
Sleep 3s
Type "fetch https://news.ycombinator.com and tell me the top story title"
Enter 1
Sleep 15s
Tab
Space
Sleep 3s
Space
Up
Space
Sleep 3s
Space
Ctrl+C
Sleep 3s
Enter 1
