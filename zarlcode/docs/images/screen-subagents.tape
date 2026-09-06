# Deterministic delegated-agent activity with the local fixture provider.
Output zarlcode/docs/images/screen-subagents.gif
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

# Enable the documented delegation tool surface before recording.
Ctrl+S
Down 4
Right
Enter
Wait+Screen /enable sub-agents/
Ctrl+S
Wait+Screen /build mode/
Show
Sleep 2s

Type "Delegate a review of the greeting refactor."
Enter
Wait+Screen@30s /Review requested/
Sleep 3s
Ctrl+A
Wait+Screen /delegated/
Sleep 4s
Screenshot zarlcode/docs/images/screen-subagents-poster.png
Sleep 500ms

Hide
Ctrl+A
Wait+Screen /build mode/
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
