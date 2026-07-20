package home

// knownLegacyPromptSeedHashes contains SHA-256 hashes of system prompts that
// zarlcode releases materialized into ~/.zarlcode/prompt.md before the prompt
// lifecycle moved to an immutable embedded core plus additive preferences.
var knownLegacyPromptSeedHashes = map[string]struct{}{
	// zarlcode/v0.1.0, zarlcode/v0.1.1, zarlcode/v0.1.2
	"f704550911e2702820d017c234f32f9ce3aaeefe263815b4b4de19c2a13fc700": {},
	// zarlcode/v0.1.3, zarlcode/v0.1.4, zarlcode/v0.1.5, zarlcode/v0.1.6
	"6480987309397968c0aaea88ae8476b9a7b550b0af533a655c75c9956d0bc9b9": {},
	// zarlcode/v0.2.0
	"3326b790a1063e1c76561756c7ecb6c6e4ed8adb8ee385b1f10dd45618ff3186": {},
	// zarlcode/v0.4.0 and the prompt present when the migration began.
	"bc2473415b813172e7a661c2c2fea403a444a3091f7284dbb1c73f4bdc9760f9": {},
}
