# Deterministic cockpit/dashboard recording using the local fixture provider.
Output zarlcode/docs/images/screen-cockpit.gif
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

Ctrl+L
Wait+Screen /context/
Sleep 4s
Screenshot zarlcode/docs/images/screen-cockpit-poster.png
Sleep 500ms

Hide
Ctrl+L
Wait+Screen /build mode/
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
