package tui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// auxiliaryObservationLines borrows only the two event-loop-owned slots. Reuse
// the existing evidence formatters without configured estimates or spend folds.
func (s *RunState) auxiliaryObservationLines(width int) []string {
	var lines []string
	for _, o := range [...]*engine.AuxiliaryObservation{s.auxiliaryObservations.Judge, s.auxiliaryObservations.SpawnPlanner} {
		if o == nil {
			continue
		}
		i := o.Inference
		valid := "invalid"
		if o.ResponseValid {
			valid = "valid"
		}
		lines = append(lines, o.Kind.String()+" · latest",
			"  stream "+i.Outcome.String(), "  response "+valid,
			fmt.Sprintf("  task %s · depth %d · duration %s", providerObservationID(string(i.TaskID)), i.Depth, i.Duration))
		// No card is attached: reported cost remains evidence, never an estimate.
		d := ProviderAttemptDiagnostic{Provider: i.Provider, Model: i.Model, Usage: i.Usage, PricingUsage: i.PricingUsage, Execution: i.Execution}
		rows := providerObservationRow(d)
		lines = append(lines, "  inference provider "+providerObservationID(i.Provider), "  inference model "+providerObservationID(i.Model))
		lines = append(lines, rows[3:]...)
		usage := "unknown"
		if i.Usage != nil {
			usage = fmt.Sprintf("input %d · output %d · total %d", i.Usage.PromptTokens, i.Usage.CompletionTokens, i.Usage.TotalTokens)
		}
		lines = append(lines, "  reported legacy tokens "+usage)
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(palette.Muted.On(line), max(width, 0), "…")
	}
	return lines
}
