package deepseek_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestConservativeTariffOnlyForVerifiedIDs(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		in, out, ok := deepseek.CostPer1k(id)
		if !ok || in != .00030 || out != .00120 {
			t.Fatalf("%s=%v/%v known=%v", id, in, out, ok)
		}
	}
	if in, out, ok := deepseek.CostPer1k("deepseek-v4-pro"); !ok || in != .00132 || out != .00396 {
		t.Fatalf("pro=%v/%v known=%v", in, out, ok)
	}
	for _, id := range []string{"deepseek-chat", "deepseek-reasoner", "deepseek-fictional", "not-deepseek-v4-pro"} {
		if _, _, ok := deepseek.CostPer1k(id); ok {
			t.Fatalf("unverified tariff for %s", id)
		}
	}
}
