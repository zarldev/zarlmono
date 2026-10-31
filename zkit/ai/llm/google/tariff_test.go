package google_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestConservativeTextTariffsUseExactModelIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id      string
		in, out float64
	}{{"gemini-3.8-flash", .0015, .0075}, {"gemini-3-flash-preview", .0005, .003}, {"gemini-3.1-flash-lite", .00025, .0015}, {"gemini-3.5-flash-lite", .0003, .0025}, {"gemini-2.5-flash-lite", .0001, .0004}, {"gemini-2.5-pro", .0025, .015}} {
		in, out, ok := google.CostPer1k(tc.id)
		if !ok || in != tc.in || out != tc.out {
			t.Fatalf("%s=%v/%v known=%v", tc.id, in, out, ok)
		}
	}
	for _, id := range []string{"gemini-3.1-flash-live-preview", "gemini-3-fictional-flash", "gemini-3-pro-preview", "gemini-2.5-flash-preview-tts", "gemini-1.5-pro"} {
		if _, _, ok := google.CostPer1k(id); ok {
			t.Fatalf("unverified tariff for %s", id)
		}
	}
}
