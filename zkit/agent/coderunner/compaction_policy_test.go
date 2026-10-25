package coderunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
)

func TestCompactionThresholdReportsEarliestStandardTrigger(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		window, reserve, want int
	}{
		{"unknown", 0, 1000, 0},
		{"force first", 10000, 1000, 6000},
		{"reserve first strictly above", 10000, 5000, 5001},
		{"no reserve", 10000, 0, 6000},
		{"reserve exceeds window", 4000, 8000, 2400},
		{"fraction rounds up", 11, 0, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := coderunner.CompactionThreshold(tc.window, tc.reserve); got != tc.want {
				t.Errorf("threshold = %d, want %d", got, tc.want)
			}
		})
	}
}
