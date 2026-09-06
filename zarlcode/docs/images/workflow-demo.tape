# Complete workflow: plan without edits, build and test, inspect, then resume.
# Render from the repository root with isolated local state.
Output zarlcode/docs/images/workflow-demo.gif
Require bash
Require go
Require curl
Set Shell bash
Set FontSize 18
Set Width 1440
Set Height 900
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 15

Hide
Type "source zarlcode/docs/images/recording-setup.sh || exit 1"
Enter
Wait@2m
Type "$REC_ROOT/zarlcode"
Enter
Wait+Screen /first-run setup/
Enter
Wait+Screen /build mode/
Show
Sleep 1s

Shift+Tab
Wait+Screen /plan mode/
Type "Plan a small refactor of the greeting. Keep its behavior."
Enter
Wait+Screen /No files changed/
Sleep 4s
Shift+Tab
Wait+Screen /build mode/
Type "Extract the greeting prefix and run the test."
Enter
Wait+Screen@30s /Ready to review/
Sleep 3s
Ctrl+W
Wait+Screen /greet.go/
Sleep 2s
Enter
Wait+Screen /diff browser/
Wait+Screen /const greeting/
Sleep 4s
Screenshot zarlcode/docs/images/workflow-demo-poster.png
Sleep 500ms
Enter
Sleep 500ms
Escape
Wait+Screen /Ready to review/
Sleep 1s

# Reopen the saved session; startup and shell commands stay off camera.
Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "$REC_ROOT/zarlcode"
Enter
Wait+Screen /sessions/
Show
Sleep 2s
Tab
Wait+Screen /resume/
Sleep 2s
Enter
Wait+Screen /Ready to review/
Sleep 4s
Screenshot zarlcode/docs/images/workflow-demo-resume.png
Sleep 500ms

Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
