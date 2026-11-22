package backends_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// The v0.7 generator parses numeric discriminators (zero-based), not the
// one-based ordinal accepted by v0.4.9. Storage uses canonical string names.
func TestProviderEnumCompatibility(t *testing.T) {
	t.Parallel()
	names := []string{"openai", "deepseek", "openai-codex", "google", "anthropic", "claude-code", "llamacpp", "ollama", "litellm"}
	values := []llm.LLMProvider{llm.LLMProviders.OPENAI, llm.LLMProviders.DEEPSEEK, llm.LLMProviders.OPENAICODEX, llm.LLMProviders.GOOGLE, llm.LLMProviders.ANTHROPIC, llm.LLMProviders.CLAUDECODE, llm.LLMProviders.LLAMACPP, llm.LLMProviders.OLLAMA, llm.LLMProviders.LITELLM}
	for i, value := range values {
		t.Run(names[i], func(t *testing.T) {
			t.Parallel()
			name := names[i]
			if value.String() != name {
				t.Fatalf("String = %q", value.String())
			}
			parsed, err := llm.ParseLLMProvider(i)
			if err != nil || parsed != value {
				t.Fatalf("numeric discriminator %d = %v, %v", i, parsed, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != `"`+name+`"` {
				t.Fatalf("JSON = %s, %v", encoded, err)
			}
			var decoded llm.LLMProvider
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
				t.Fatalf("decode JSON = %v, %v", decoded, err)
			}
			sqlValue, err := value.Value()
			if err != nil || sqlValue != name {
				t.Fatalf("SQL Value = %v, %v", sqlValue, err)
			}
			for _, stored := range []any{name, []byte(name)} {
				if err := decoded.Scan(stored); err != nil || decoded != value {
					t.Fatalf("SQL Scan = %v, %v", decoded, err)
				}
			}
			for _, old := range [][]byte{[]byte(name), []byte(`"` + name + `"`)} {
				if err := decoded.UnmarshalBinary(old); err != nil || decoded != value {
					t.Fatalf("binary decode = %v, %v", decoded, err)
				}
			}
			binary, err := value.MarshalBinary()
			if err != nil || string(binary) != name {
				t.Fatalf("binary = %s, %v", binary, err)
			}
			text, err := value.MarshalText()
			if err != nil || string(text) != name {
				t.Fatalf("text = %s, %v", text, err)
			}
		})
	}
	for _, invalid := range []any{-1, len(values), 1.5, math.NaN(), math.Inf(1), uint64(math.MaxUint64)} {
		if _, err := llm.ParseLLMProvider(invalid); err == nil {
			t.Fatalf("numeric input %v accepted", invalid)
		}
	}
}

func TestAdapterEnumCompatibility(t *testing.T) {
	t.Parallel()
	names := []string{"openAICompatible", "deepSeekCompatible", "anthropicCompatible", "googleCompatible", "oauthOpenAICodex", "oauthClaudeCode", "googleVertex", "litellm"}
	values := []backends.AdapterType{backends.AdapterTypes.OPENAICOMPATIBLE, backends.AdapterTypes.DEEPSEEKCOMPATIBLE, backends.AdapterTypes.ANTHROPICCOMPATIBLE, backends.AdapterTypes.GOOGLECOMPATIBLE, backends.AdapterTypes.OAUTHOPENAICODEX, backends.AdapterTypes.OAUTHCLAUDECODE, backends.AdapterTypes.GOOGLEVERTEX, backends.AdapterTypes.LITELLM}
	for i, value := range values {
		t.Run(names[i], func(t *testing.T) {
			t.Parallel()
			name := names[i]
			if value.String() != name {
				t.Fatalf("String = %q", value.String())
			}
			parsed, err := backends.ParseAdapterType(i)
			if err != nil || parsed != value {
				t.Fatalf("numeric discriminator %d = %v, %v", i, parsed, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != `"`+name+`"` {
				t.Fatalf("JSON = %s, %v", encoded, err)
			}
			var decoded backends.AdapterType
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
				t.Fatalf("decode JSON = %v, %v", decoded, err)
			}
			sqlValue, err := value.Value()
			if err != nil || sqlValue != name {
				t.Fatalf("SQL Value = %v, %v", sqlValue, err)
			}
			for _, stored := range []any{name, []byte(name)} {
				if err := decoded.Scan(stored); err != nil || decoded != value {
					t.Fatalf("SQL Scan = %v, %v", decoded, err)
				}
			}
			for _, old := range [][]byte{[]byte(name), []byte(`"` + name + `"`)} {
				if err := decoded.UnmarshalBinary(old); err != nil || decoded != value {
					t.Fatalf("binary decode = %v, %v", decoded, err)
				}
			}
			binary, err := value.MarshalBinary()
			if err != nil || string(binary) != name {
				t.Fatalf("binary = %s, %v", binary, err)
			}
			text, err := value.MarshalText()
			if err != nil || string(text) != name {
				t.Fatalf("text = %s, %v", text, err)
			}
		})
	}
	for _, invalid := range []any{-1, len(values), 1.5, math.NaN(), math.Inf(1), uint64(math.MaxUint64)} {
		if value, _ := backends.ParseAdapterType(invalid); value.IsValid() {
			t.Fatalf("numeric input %v accepted", invalid)
		}
	}
}
