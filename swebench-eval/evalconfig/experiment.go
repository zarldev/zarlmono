package evalconfig

import (
	"fmt"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func loadExperiment(cfg *Config, path string) error {
	if cfg.Refinement.Enabled {
		return validateRefinement(*cfg, path)
	}
	if path == "" {
		if cfg.Persistence.EvidenceDir != "" {
			return fmt.Errorf("%w: --harness-evidence-dir requires --harness-experiment", harness.ErrExperiment)
		}
		return nil
	}
	if cfg.Persistence.EvidenceDir == "" || strings.TrimSpace(cfg.Zarlcode.Provider) == "" || strings.TrimSpace(cfg.Zarlcode.Model) == "" {
		return fmt.Errorf("%w: require --harness-evidence-dir, --zarlcode-provider and --zarlcode-model", harness.ErrExperiment)
	}
	if strings.TrimSpace(cfg.Input.Drivers) != "zarlcode" || cfg.Input.AblationSpec != "" || cfg.Zarlcode.VerifiedAttempts > 1 || cfg.Zarlcode.VerifiedAttempts < 0 || cfg.Zarlcode.ThreadTranscript {
		return fmt.Errorf("%w: trials require only zarlcode, no ablations or verified re-drive", harness.ErrExperiment)
	}
	if cfg.Execution.TaskTimeout <= 0 || (cfg.Zarlcode.LlamacppResetURL != "" && cfg.Execution.Concurrency > 1) {
		return fmt.Errorf("%w: require a positive task timeout and serialize trials when resetting a shared model slot", harness.ErrExperiment)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open harness experiment: %w", err)
	}
	defer f.Close()
	cfg.Experiment, err = harness.ParseExperiment(f)
	return err
}
