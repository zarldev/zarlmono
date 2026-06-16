// Package claudecode adapts the OAuth-backed Claude Code CLI surface to the
// shared llm.Provider interface.
//
// The provider invokes the Claude Code Agent SDK command-line surface rather
// than the public Anthropic API. It is useful for consumers that intentionally
// want Claude Code's OAuth/product integration while still speaking zkit's
// provider-neutral streaming contract.
//
// Because this package depends on a product CLI and OAuth token lifecycle, its
// upstream behavior is more volatile than official API providers. Keep the core
// llm.Provider contract stable and isolate Claude Code-specific behavior here.
package claudecode
