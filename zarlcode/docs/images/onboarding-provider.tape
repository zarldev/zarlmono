# Deterministic provider and vault onboarding with synthetic credentials only.
# Render from the repository root.
Output zarlcode/docs/images/onboarding-provider.gif
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
Sleep 2s

Enter
Wait+Screen /build mode/
Sleep 1s
Ctrl+S
Wait+Screen /\[settings\]/
Sleep 2s
Down
Enter
Wait+Screen /key\/sign-in/
Sleep 2s
Enter
Type "smoke-secret"
Enter
Wait+Screen /vault unlock/
Sleep 2s
Type "smoke-passphrase"
Enter
Wait+Screen /confirm/
Sleep 2s
Type "smoke-passphrase"
Enter
Wait+Screen /openai key saved \(global\)/
Screenshot zarlcode/docs/images/onboarding-provider-poster.png
Sleep 3s
Ctrl+S
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
