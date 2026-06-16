// Package provider defines the registry for LLM providers (both built-in
// and DB-backed) and the generic adapter layer that configures them.
package backends

//go:generate go tool goenums enums.go

type adapterType int

const (
	openAICompatible adapterType = iota
	deepSeekCompatible
	anthropicCompatible
	googleCompatible
	oauthOpenAICodex
	oauthClaudeCode
	// googleVertex is appended (not grouped with googleCompatible) so the
	// existing iota values — and any state that captured them — stay put.
	googleVertex
)
