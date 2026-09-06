# Inspect the source and its behavior test after establishing the package context.
Output zarlcode/docs/images/screen-fileviewer.gif
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
Sleep 3s

Ctrl+F
Wait+Screen /files/
Wait+Screen /greet_test.go/
Down 2
Wait+Screen /func Message/
Sleep 3s
Down
Wait+Screen /func TestMessage/
Sleep 4s
Screenshot zarlcode/docs/images/screen-fileviewer-poster.png
Sleep 500ms

Hide
Escape
Wait+Screen /build mode/
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
