// Command coderisk reports complexity and coverage from a completed code-risk
// collection. Run go tool task code-risk to collect all workspace modules first.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/tools/coderisk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coderisk:", err)
		os.Exit(1)
	}
}

func run() error {
	input := flag.String("input", "", "completed collection directory from task code-risk")
	root := flag.String("root", ".", "workspace root matching the collected sources")
	top := flag.Int("top", 30, "rows per Markdown section; 0 shows all (JSON always includes all)")
	flag.Parse()
	if *input == "" || *top < 0 || flag.NArg() != 0 {
		return errors.New("usage: coderisk -input directory [-root workspace] [-top nonnegative-count]")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if _, err := os.Stat(filepath.Join(*input, "complete")); err != nil {
		return fmt.Errorf("collection is incomplete: %w", err)
	}
	environment, err := os.ReadFile(filepath.Join(*input, "environment.json"))
	if err != nil {
		return fmt.Errorf("read collection environment: %w", err)
	}
	report := coderisk.Report{Modules: []string{"root (tools)", "examples", "zkit", "zarlcode", "swebench-eval"}}
	if err := json.Unmarshal(environment, &report.Environment); err != nil {
		return fmt.Errorf("decode collection environment: %w", err)
	}
	for _, module := range []string{"root", "examples", "zkit", "zarlcode", "swebench-eval"} {
		functions, err := analyzeModule(absoluteRoot, *input, module)
		if err != nil {
			return fmt.Errorf("analyze %s: %w", module, err)
		}
		report.Functions = append(report.Functions, functions...)
	}
	coderisk.Rank(report.Functions)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*input, "report.json"), append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*input, "report.md"), []byte(report.Markdown(*top)), 0o600); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Report: %s\nFull data: %s\n", filepath.Join(*input, "report.md"), filepath.Join(*input, "report.json"))
	return nil
}

func analyzeModule(root, input, module string) ([]coderisk.Function, error) {
	packages, err := os.ReadFile(filepath.Join(input, module+".packages.json"))
	if err != nil {
		return nil, fmt.Errorf("read package inventory: %w", err)
	}
	profile, err := os.ReadFile(filepath.Join(input, module+".cover"))
	if err != nil {
		return nil, fmt.Errorf("read coverage: %w", err)
	}
	return coderisk.Analyze(root, bytes.NewReader(packages), bytes.NewReader(profile))
}
