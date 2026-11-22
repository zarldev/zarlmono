package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const providerObservationRowLimit = 3

// providerObservationLines borrows the event-loop-owned, settled diagnostics.
// Legacy usage alone supplies no presence-aware execution evidence.
func (s *RunState) providerObservationLines(width int) []string {
	count := 0
	for _, d := range s.providerObservations {
		if hasProviderObservation(d) {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	lines := []string{"settled attempts · evidence, not session spend"}
	omitted := max(count-providerObservationRowLimit, 0)
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("%d earlier observations omitted", omitted))
	}
	for _, d := range s.providerObservations {
		if !hasProviderObservation(d) {
			continue
		}
		if omitted > 0 {
			omitted--
			continue
		}
		lines = append(lines, providerObservationRow(d)...)
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(palette.Muted.On(line), max(width, 0), "…")
	}
	return lines
}

func hasProviderObservation(d ProviderAttemptDiagnostic) bool {
	if d.RateCard != nil {
		return true
	}
	read, write := providerPromptCache(d)
	if read.Known || write.Known {
		return true
	}
	e := d.Execution
	return e != nil && (e.GatewayDeployment != "" || e.ReportedModel != "" ||
		e.ReportedVersion != "" || e.GatewayVersion != "" || e.ReportedCost.Reported)
}

func providerObservationRow(d ProviderAttemptDiagnostic) []string {
	var e llm.ExecutionObservation
	if d.Execution != nil {
		e = *d.Execution
	}
	lines := []string{
		fmt.Sprintf("task %s · depth %d · attempt %d", providerObservationID(d.TaskID), d.Depth, d.Attempt),
		"  requested provider " + providerObservationID(d.Provider),
		"  requested alias " + providerObservationID(d.Model),
		"  observed deployment " + providerObservationID(string(e.GatewayDeployment)),
		"  reported model " + providerObservationID(string(e.ReportedModel)),
		"  gateway runtime version " + providerObservationID(string(e.GatewayVersion)),
	}
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
	read, write := providerPromptCache(d)
	lines = append(lines,
		"  reported cost "+cost,
		"  reported cost coverage "+coverage,
		"  prompt-cache read tokens "+providerObservationTokens(read),
		"  prompt-cache write tokens "+providerObservationTokens(write),
	)
	return append(lines, providerConfiguredEstimateLines(d)...)
}

// Explicit execution counts take precedence, including zero. Pricing sidecars
// also carry known counts; legacy Usage cannot distinguish zero from absence.
func providerPromptCache(d ProviderAttemptDiagnostic) (llm.TokenQuantity, llm.TokenQuantity) {
	var read, write llm.TokenQuantity
	if d.PricingUsage != nil {
		read, write = d.PricingUsage.Input.CacheRead, d.PricingUsage.Input.CacheWrite
	}
	if d.Execution != nil {
		if d.Execution.PromptCacheReadTokens.Known {
			read = d.Execution.PromptCacheReadTokens
		}
		if d.Execution.PromptCacheWriteTokens.Known {
			write = d.Execution.PromptCacheWriteTokens
		}
	}
	return read, write
}

func providerObservationTokens(q llm.TokenQuantity) string {
	if !q.Known {
		return "unknown"
	}
	return strconv.FormatInt(q.Tokens, 10)
}

func providerObservationID(id string) string {
	if id == "" {
		return "unknown"
	}
	// Quote preserves whitespace/case and escapes line breaks and tabs. Reuse
	// the terminal-control helper before applying any host styling.
	return escapeToolInputControls(strconv.Quote(id))
}

// Configured estimates never borrow the currency of independently reported cost
// and are not folded into the legacy base-rate estimate or session invoice.
func providerConfiguredEstimateLines(d ProviderAttemptDiagnostic) []string {
	if d.RateCard == nil {
		return nil
	}
	card := d.RateCard
	lines := []string{
		"  configured estimate · not reported cost/invoice",
		"  basis " + providerObservationID(card.Reference.ID),
		"  basis revision " + providerObservationID(card.Reference.Revision),
		"  basis entry " + providerObservationID(card.Reference.Entry),
		"  pricing provider " + providerObservationID(string(card.PricingProvider)),
		"  estimate currency " + providerObservationID(string(card.Currency)),
	}
	if d.ConfiguredEstimate == nil {
		return append(lines, "  estimate unavailable: missing normalized usage", "  estimate coverage unavailable")
	}
	estimate := d.ConfiguredEstimate
	amount := "unknown"
	if estimate.HasKnownSubtotal {
		amount = strconv.FormatFloat(estimate.Subtotal, 'g', 6, 64)
	}
	coverage := "incomplete"
	if estimate.Complete {
		coverage = "complete (configured tokens)"
	}
	lines = append(lines, "  configured subtotal "+amount, "  estimate coverage "+coverage)
	for _, issue := range estimate.Issues {
		lines = append(lines, "  estimate issue "+issue.Kind.String()+" · "+providerObservationID(issue.Field))
	}
	return lines
}
