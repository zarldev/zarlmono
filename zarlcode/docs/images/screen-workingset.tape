# Deterministic working set: local edit, diff browser, and rollback.
Output zarlcode/docs/images/screen-workingset.gif
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
Sleep 2s

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
Screenshot zarlcode/docs/images/screen-workingset-poster.png
Sleep 500ms
Type "r"
Wait+Screen /Rollback/
Sleep 2s
Enter
Sleep 500ms
Enter
Sleep 500ms
Escape
Wait+Screen /build mode/
Wait+Screen /rolled back greet.go/
Sleep 3s
Screenshot zarlcode/docs/images/screen-workingset-rollback.png
Sleep 500ms

Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
