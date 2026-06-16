// Package codex implements the OpenAI Codex half of zkit/oauth: the
// PKCE authorization flow with a loopback callback server on the
// Codex-registered redirect (localhost:1455), a manual paste fallback,
// and an openaicodex.TokenSource that refreshes proactively near expiry
// and persists back to the scope the credential was read from.
package codex
