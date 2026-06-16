// Package claude implements the Claude Code half of zkit/oauth: the
// `claude setup-token` sign-in flow and a claudecode.TokenSource backed
// by the prefs vault. There is no callback server here — Claude Code's
// own CLI drives the browser interaction and prints the token; this
// package captures, extracts, and stores it.
package claude
