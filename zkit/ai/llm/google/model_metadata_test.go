package google_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestCurrentDefaultMetadataDoesNotBorrowOlderModelRates(t *testing.T) {
	t.Parallel()
	input, output, known := google.CostPer1k("gemini-3.8-flash")
	if !known || input != 0.00075 || output != 0.00375 {
		t.Fatalf("current standard estimate=%g/%g known=%v", input, output, known)
	}
	if !google.Capabilities("gemini-3.8-flash").SupportsThinking {
		t.Fatal("current default lost thinking capability")
	}
	if _, _, known := google.CostPer1k("gemini-unlisted-future-model"); known {
		t.Fatal("unknown model silently inherited an older generation's rates")
	}
}
