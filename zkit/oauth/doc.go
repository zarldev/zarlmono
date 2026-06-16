// Package oauth turns a subscription into a usable LLM credential. The
// provider-specific flows live in the subpackages, mirroring the
// golang.org/x/oauth2 shape:
//
//   - [github.com/zarldev/zarlmono/zkit/oauth/codex] — the OpenAI Codex
//     flow: PKCE + loopback callback server + browser hand-off, and the
//     refreshing token source.
//   - [github.com/zarldev/zarlmono/zkit/oauth/claude] — the Claude Code
//     flow: `claude setup-token` wrapping and the vault-backed token
//     source.
//
// Tokens land in the prefs vault — encrypted at rest, scoped per
// workspace — and the token sources refresh or re-read them lazily, so
// consumers hold a stable source rather than a credential snapshot. The
// root package carries only [RunLogin], the provider-string dispatcher
// the CLI uses.
//
// The flows live here, beside the storage they bind to, rather than in
// the provider packages: claudecode and openaicodex define the
// TokenSource contracts they consume, and keeping the implementations
// out keeps the providers free of the vault/db dependency tree.
package oauth
