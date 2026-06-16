// Package report renders evaluation Results in human / machine-
// readable shapes. Console output is for the dev loop; markdown +
// CSV are for the comparison artefacts you commit alongside the
// numbers.
//
// Note: a result row's Diff being non-empty doesn't mean the task
// resolved — SWE-bench scoring is a downstream step that runs the
// official evaluator against each diff. This package only reports
// what the harnesses *produced*; the resolution column gets
// populated by a separate "score" command that consumes Results +
// SWE-bench's evaluator output.
package report

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/runner"
)

// Console writes a per-driver summary table to w, followed by the
// per-task details. Suitable for dev iteration when you want to see
// what changed after a tweak without opening a markdown viewer.
func Console(w io.Writer, r runner.Results) {
	fmt.Fprintln(w, "=== swebench-eval summary ===")
	fmt.Fprintf(w, "tasks:    %d\n", countTasks(r))
	fmt.Fprintf(w, "drivers:  %d\n", countDrivers(r))
	fmt.Fprintf(w, "duration: %s\n", r.Duration().Round(1e8))
	fmt.Fprintln(w)

	// Per-driver aggregate. The label includes provider/model when
	// every record for that driver agrees on a single pair (the common
	// case — one run, one backend). When records disagree (rare,
	// happens only if the driver was reconfigured mid-run), the label
	// reads "<driver> (multiple)" and the per-task rows below show
	// the actual values.
	//
	// Resolved is shown when at least one record has been scored;
	// otherwise the column reads "—" to keep the table honest about
	// "we didn't run the evaluator".
	fmt.Fprintln(w, "per-driver:")
	fmt.Fprintf(w, "  %-32s %8s %8s %10s %10s %12s %12s %10s %8s\n",
		"driver / provider / model", "runs", "diffs", "resolved", "verified", "tot dur", "avg dur", "errors", "g-rej")
	for _, d := range driverNames(r) {
		var runs, withDiff, errs, scored, resolved, rejections, verified int
		var totDur time.Duration
		providers := map[string]struct{}{}
		models := map[string]struct{}{}
		for _, rec := range r.Records {
			if rec.DriverName != d {
				continue
			}
			runs++
			if rec.Result.Diff != "" {
				withDiff++
			}
			if rec.Result.Err != nil {
				errs++
			}
			if rec.Resolved != nil {
				scored++
				if *rec.Resolved {
					resolved++
				}
			}
			for _, n := range rec.Result.GuardrailRejections {
				rejections += n
			}
			if rec.Result.Verified {
				verified++
			}
			totDur += rec.Result.Duration
			if rec.Result.Provider != "" {
				providers[rec.Result.Provider] = struct{}{}
			}
			if rec.Result.Model != "" {
				models[rec.Result.Model] = struct{}{}
			}
		}
		var avgDur time.Duration
		if runs > 0 {
			avgDur = totDur / time.Duration(runs)
		}
		resolvedCol := "—"
		if scored > 0 {
			pct := 100 * float64(resolved) / float64(scored)
			resolvedCol = fmt.Sprintf("%d/%d %.0f%%", resolved, scored, pct)
		}
		label := d
		switch {
		case len(providers) == 0 && len(models) == 0:
			// Pre-migration data or driver doesn't surface backend
			// metadata — leave the bare driver name.
		case len(providers) <= 1 && len(models) <= 1:
			label = fmt.Sprintf("%s / %s / %s", d, soleKey(providers), soleKey(models))
		default:
			label = fmt.Sprintf("%s (multiple)", d)
		}
		fmt.Fprintf(w, "  %-32s %8d %8d %10s %10d %12s %12s %10d %8d\n",
			label, runs, withDiff, resolvedCol, verified,
			formatDur(int64(totDur)),
			formatDur(int64(avgDur)),
			errs, rejections)
	}
	fmt.Fprintln(w)

	// Per-task details, grouped by language for readability.
	byLang := map[string][]runner.TaskResult{}
	for _, rec := range r.Records {
		byLang[rec.Language] = append(byLang[rec.Language], rec)
	}
	for _, lang := range sortedKeys(byLang) {
		fmt.Fprintf(w, "language: %s (%d records)\n", lang, len(byLang[lang]))
		for _, rec := range byLang[lang] {
			status := "ok"
			if rec.Result.Err != nil {
				status = "ERR: " + rec.Result.Err.Error()
			} else if rec.Result.Diff == "" {
				status = "no-change"
			}
			fmt.Fprintf(w, "  [%-12s] %-50s %12s  iter=%-3d tools=%-3d  %s\n",
				rec.DriverName, rec.InstanceID,
				formatDur(int64(rec.Result.Duration)),
				rec.Result.Iterations, rec.Result.ToolCalls,
				status)
		}
		fmt.Fprintln(w)
	}
}

// countTasks returns the unique-InstanceID count across all records.
func countTasks(r runner.Results) int {
	seen := map[string]bool{}
	for _, rec := range r.Records {
		seen[rec.InstanceID] = true
	}
	return len(seen)
}

func countDrivers(r runner.Results) int {
	seen := map[string]bool{}
	for _, rec := range r.Records {
		seen[rec.DriverName] = true
	}
	return len(seen)
}

func driverNames(r runner.Results) []string {
	seen := map[string]bool{}
	for _, rec := range r.Records {
		seen[rec.DriverName] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// soleKey returns the single key of a 0-or-1-element set, or "" for
// an empty set. Used by the per-driver label formatter when a driver's
// records all agree on a single provider (or model) value.
func soleKey(m map[string]struct{}) string {
	for k := range m {
		return k
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// formatDur renders a nanosecond duration as the largest sensible
// unit: "4.2s" / "120ms" / "3m12s". Hand-rolled so the column widths
// stay predictable across rows.
func formatDur(ns int64) string {
	if ns < 0 {
		return "?"
	}
	if ns < 1_000_000 {
		return fmt.Sprintf("%dµs", ns/1_000)
	}
	if ns < 1_000_000_000 {
		return fmt.Sprintf("%dms", ns/1_000_000)
	}
	if ns < 60_000_000_000 {
		return fmt.Sprintf("%.1fs", float64(ns)/1e9)
	}
	mins := ns / 60_000_000_000
	sec := (ns % 60_000_000_000) / 1_000_000_000
	return fmt.Sprintf("%dm%ds", mins, sec)
}
