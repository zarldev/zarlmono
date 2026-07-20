package home

import "testing"

func TestKnownLegacyPromptSeedHashesIncludeReleasedPrompts(t *testing.T) {
	for _, hash := range []string{
		"f704550911e2702820d017c234f32f9ce3aaeefe263815b4b4de19c2a13fc700",
		"6480987309397968c0aaea88ae8476b9a7b550b0af533a655c75c9956d0bc9b9",
		"3326b790a1063e1c76561756c7ecb6c6e4ed8adb8ee385b1f10dd45618ff3186",
		"bc2473415b813172e7a661c2c2fea403a444a3091f7284dbb1c73f4bdc9760f9",
	} {
		if _, ok := knownLegacyPromptSeedHashes[hash]; !ok {
			t.Fatalf("knownLegacyPromptSeedHashes missing %s", hash)
		}
	}
}
