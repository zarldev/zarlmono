# Deterministic model picker: use the local fixture's single discovered model.
Output zarlcode/docs/images/screen-modelpicker.gif
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
Type "Explain this greeting package before we change it."
Enter
Wait+Screen /A small Go package/
Show
Sleep 2s

Ctrl+E
Wait+Screen /zarlcode-recording-model/
Sleep 4s
Screenshot zarlcode/docs/images/screen-modelpicker-poster.png
Sleep 500ms
Escape
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
