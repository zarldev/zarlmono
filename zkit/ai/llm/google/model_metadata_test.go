package google_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestCurrentDefaultMetadataDoesNotBorrowOlderModelRates(t *testing.T) {
	t.Parallel()
	input, output, known := google.CostPer1k("gemini-3.8-flash")
	if !known || input != 0.0015 || output != 0.0075 {
		t.Fatalf("current conservative estimate=%g/%g known=%v", input, output, known)
	}
	if !google.Capabilities("gemini-3.8-flash").SupportsThinking {
		t.Fatal("current default lost thinking capability")
	}
	if _, _, known := google.CostPer1k("gemini-unlisted-future-model"); known {
		t.Fatal("unknown model silently inherited an older generation's rates")
	}
}

func TestCapabilitiesPreserveModelsPrefixEquivalence(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gemini-3.8-flash", "gemini-4-argon", "gemini-4-hydrogen", "gemini-2.5-flash", "gemini-unlisted"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			want := google.Capabilities(model)
			if got := google.Capabilities("models/" + model); got != want {
				t.Fatalf("prefixed capabilities = %+v, want bare model capabilities %+v", got, want)
			}
		})
	}
}
