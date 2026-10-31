package coderunner

import "math"

// CompactionThreshold reports the earliest provider-usage threshold at which
// the standard policy can invoke compaction. Reduction still depends on eligible
// history. Zero means the model window is unknown. The force path uses prompt
// tokens; the reserve gate uses total tokens, so this is a policy marker rather
// than a prediction of the next request's occupancy.
func CompactionThreshold(window, reserve int) int {
	if window <= 0 {
		return 0
	}
	threshold := int(math.Ceil(float64(window) * TokenPressureFraction))
	if gate := window - reserve; gate > 0 && gate < window {
		// PressureGated opens strictly above the reserve threshold.
		threshold = min(threshold, gate+1)
	}
	return threshold
}
