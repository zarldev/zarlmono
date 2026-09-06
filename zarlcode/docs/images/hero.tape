# Product story: an approved plan, real edit/test tools, then the one-file diff.
# The poster is captured at a named, populated review state, never a timestamp.
Output zarlcode/docs/images/hero.gif
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
Shift+Tab
Wait+Screen /plan mode/
Type "Plan a small refactor of the greeting. Keep its behavior."
Enter
Wait+Screen /No files changed/
Show
Sleep 3s
Shift+Tab
Wait+Screen /build mode/
Type "Extract the greeting prefix and run the test."
Enter
Wait+Screen@30s /Ready to review/
Sleep 4s
Screenshot zarlcode/docs/images/hero-poster.png
Sleep 500ms
Ctrl+W
Wait+Screen /greet.go/
Sleep 2s
Enter
Wait+Screen /diff browser/
Wait+Screen /const greeting/
Sleep 4s
Screenshot zarlcode/docs/images/hero-diff.png
Sleep 500ms
Enter
Sleep 500ms
Escape
Wait+Screen /Ready to review/
Sleep 2s

Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
