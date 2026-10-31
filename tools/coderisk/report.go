package coderisk

import (
	"fmt"
	"strings"
)

// Report records the measured population and collection environment. Functions
// must be ranked with Rank before rendering the Markdown summary.
type Report struct {
	Environment map[string]string `json:"environment"`
	Modules     []string          `json:"modules"`
	Functions   []Function        `json:"functions"`
}

// Markdown renders the highest-risk measured functions and a separate list of
// the most complex unmeasured functions. A nonpositive limit displays all rows.
func (r Report) Markdown(limit int) string {
	var out strings.Builder
	fmt.Fprintln(&out, "# Go change-risk baseline")
	fmt.Fprintf(&out, "\nScope: %s. Environment: %s %s/%s; CGO_ENABLED=%s; GOFLAGS=%q.\n",
		strings.Join(r.Modules, ", "), r.Environment["GOVERSION"], r.Environment["GOOS"],
		r.Environment["GOARCH"], r.Environment["CGO_ENABLED"], r.Environment["GOFLAGS"])
	measured := 0
	for _, fn := range r.Functions {
		if fn.CRAP != nil {
			measured++
		}
	}
	fmt.Fprintf(&out, "\n%d named functions: **%d measured**, **%d unmeasured**.\n",
		len(r.Functions), measured, len(r.Functions)-measured)
	fmt.Fprintln(&out, "\nReport only: no numerical quality gate. CRAP = CC² × (1 − covered/total statements)³ + CC.")
	fmt.Fprintln(&out, "\nComplexity uses gocyclo: base 1 plus if, for/range, non-default switch/select cases, && and ||. Nested closures belong to the enclosing named function for both metrics; package-level function literals are omitted. Only active build files are included; generated files, tests and bodyless declarations are excluded.")
	fmt.Fprintln(&out, "\nCoverage is statement-weighted, not branch or concurrency coverage. Tests instrument each package itself (no cross-package -coverpkg). Unknown coverage or zero executable statements produce null metrics, not a fabricated score. Use raw, matching inventories/profiles and unchanged sources when regenerating. Scores prioritize investigation; they do not prove correctness or justify mechanical refactors.")
	fmt.Fprintln(&out, "\n## Highest measured risk")
	fmt.Fprintln(&out, "\n| Function | Source | CC | Statements | Coverage | CRAP |")
	fmt.Fprintln(&out, "| --- | --- | ---: | ---: | ---: | ---: |")
	shown := 0
	for _, fn := range r.Functions {
		if fn.CRAP == nil || limit > 0 && shown >= limit {
			continue
		}
		fmt.Fprintf(&out, "| `%s` | `%s:%d:%d` | %d | %d/%d | %.1f%% | %.2f |\n",
			fn.Name, fn.File, fn.Line, fn.Column, fn.Complexity, fn.Covered,
			fn.Statements, *fn.Coverage, *fn.CRAP)
		shown++
	}
	if measured < len(r.Functions) {
		fmt.Fprintln(&out, "\n## Unmeasured (highest complexity first)")
		fmt.Fprintln(&out, "\nMissing coverage or no executable statements; inspect these separately, not as low-risk functions.")
		fmt.Fprintln(&out, "\n| Function | Source | CC |")
		fmt.Fprintln(&out, "| --- | --- | ---: |")
		shown = 0
		for _, fn := range r.Functions {
			if fn.CRAP != nil || limit > 0 && shown >= limit {
				continue
			}
			fmt.Fprintf(&out, "| `%s` | `%s:%d:%d` | %d |\n", fn.Name, fn.File, fn.Line, fn.Column, fn.Complexity)
			shown++
		}
	}
	return out.String()
}
