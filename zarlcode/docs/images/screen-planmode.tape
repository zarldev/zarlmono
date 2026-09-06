# Deterministic Plan → Build handoff using a local fixture response.
Output zarlcode/docs/images/screen-planmode.gif
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

Shift+Tab
Wait+Screen /plan mode/
Sleep 2s
Type "Plan a small refactor of the greeting. Keep its behavior."
Enter
Wait+Screen /No files changed/
Sleep 4s
Screenshot zarlcode/docs/images/screen-planmode-poster.png
Sleep 500ms
Shift+Tab
Wait+Screen /build mode/
Sleep 2s

Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
