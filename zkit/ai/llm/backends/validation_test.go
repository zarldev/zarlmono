package backends_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestValidateDefinition(t *testing.T) {
	valid := backends.ProviderDefinition{
		Name:        "custom-openai",
		AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:     "https://example.com/v1",
		Enabled:     true,
	}
	if err := backends.ValidateDefinition(valid); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}

	cases := []backends.ProviderDefinition{
		{Name: "Bad Name", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "https://example.com/v1"},
		{Name: "no-base", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE},
		{Name: "leaky", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "https://user:pass@example.com/v1"},
		{Name: "bad-url", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "not-a-url"},
	}
	for _, tc := range cases {
		if err := backends.ValidateDefinition(tc); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("ValidateDefinition(%+v) = %v, want ErrInvalidProviderDef", tc, err)
		}
	}
}
