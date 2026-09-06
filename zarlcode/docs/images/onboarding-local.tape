# Deterministic local-default onboarding with the local recording fixture.
# Render from the repository root.
Output zarlcode/docs/images/onboarding-local.gif
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
Show
Sleep 3s

Enter
Wait+Screen /build mode/
Sleep 3s
Screenshot zarlcode/docs/images/onboarding-local-poster.png
Sleep 500ms

Hide
Ctrl+C
Wait+Screen /quit ƶarl\/code\?/
Enter
Wait
Type "recording_cleanup"
Enter
Wait
