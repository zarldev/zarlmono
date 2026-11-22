package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// compactionObservationLines renders auxiliary evidence independently of task
// attempts and configured estimates. Even absent sidecars retain attribution.
func (s *RunState) compactionObservationLines(width int) []string {
	var lines []string
	for _, d := range s.compactionObservations {
		status := "settled"
		if d.Failed {
			status = "failed"
		}
		lines = append(lines,
			fmt.Sprintf("task %s · depth %d · %s", providerObservationID(d.TaskID), d.Depth, status),
			"  inference provider "+providerObservationID(d.Inference.Provider),
			"  inference model "+providerObservationID(d.Inference.Model),
		)
		var e llm.ExecutionObservation
		if d.Inference.Execution != nil {
			e = *d.Inference.Execution
		}
		lines = append(lines,
			"  observed deployment "+providerObservationID(string(e.GatewayDeployment)),
			"  reported model "+providerObservationID(string(e.ReportedModel)),
			"  gateway runtime version "+providerObservationID(string(e.GatewayVersion)),
		)
		if e.ReportedVersion != "" {
			lines = append(lines, "  reported model version "+providerObservationID(string(e.ReportedVersion)))
		}
		cost := "unreported"
		if e.ReportedCost.Reported {
			currency := "currency unknown"
			if e.ReportedCost.Currency != "" {
				currency = "currency " + providerObservationID(string(e.ReportedCost.Currency))
			}
			cost = strconv.FormatFloat(e.ReportedCost.Amount, 'g', -1, 64) + " · " + currency
		}
		coverage := "unknown"
		if e.ReportedCost.Reported && e.ReportedCost.CoverageKnown {
			coverage = "known"
		}
		read, write := e.PromptCacheReadTokens, e.PromptCacheWriteTokens
		if u := d.Inference.PricingUsage; u != nil {
			if !read.Known {
				read = u.Input.CacheRead
			}
			if !write.Known {
				write = u.Input.CacheWrite
			}
		}
		lines = append(lines,
			"  reported cost "+cost,
			"  reported cost coverage "+coverage,
			"  prompt-cache read tokens "+providerObservationTokens(read),
			"  prompt-cache write tokens "+providerObservationTokens(write),
		)
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(palette.Muted.On(line), max(width, 0), "…")
	}
	return lines
}
