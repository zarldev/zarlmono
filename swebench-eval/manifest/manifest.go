// Package manifest captures versioned evaluation inputs without copying credentials or environment contents.
package manifest

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"

	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/task"
	"github.com/zarldev/zarlmono/swebench-eval/version"
)

type runManifest struct {
	FormatVersion int               `json:"format_version"`
	Build         buildIdentity     `json:"build"`
	Tasks         []task.Spec       `json:"tasks"`
	TasksSHA256   string            `json:"tasks_sha256"`
	Drivers       []string          `json:"drivers"`
	Requested     requestedSettings `json:"requested"`
}

type buildIdentity struct {
	Version    string           `json:"version"`
	GoVersion  string           `json:"go_version"`
	OS         string           `json:"os"`
	Arch       string           `json:"arch"`
	Revision   string           `json:"vcs_revision,omitempty"`
	RevisionAt string           `json:"vcs_time,omitempty"`
	Modified   *bool            `json:"vcs_modified"`
	Modules    []moduleIdentity `json:"modules"`
}

type moduleIdentity struct {
	Path        string       `json:"path"`
	Version     string       `json:"version"`
	Sum         string       `json:"sum,omitempty"`
	Replacement *replacement `json:"replacement,omitempty"`
}

type replacement struct {
	Local   bool   `json:"local"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Sum     string `json:"sum,omitempty"`
}

// requestedSettings deliberately excludes raw argv, paths, endpoints, environment
// contents, and stored provider configuration. Zero values retain CLI semantics;
// they are not claims about effective provider or runner defaults.
type requestedSettings struct {
	Languages           string `json:"languages"`
	Sample              int    `json:"sample"`
	Ablations           string `json:"ablations"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	CodexEffort         string `json:"codex_effort"`
	TaskTimeout         string `json:"task_timeout"`
	Concurrency         int    `json:"concurrency"`
	MaxIterations       int    `json:"max_iterations"`
	ToolConcurrency     int    `json:"tool_concurrency"`
	ContextWindow       int    `json:"context_window"`
	StreamIdle          string `json:"stream_idle"`
	IterationTimeout    string `json:"iteration_timeout"`
	DeadlineGrace       string `json:"deadline_grace"`
	VerifiedAttempts    int    `json:"verified_attempts"`
	VerifyWorkers       int    `json:"verify_workers"`
	VerifyTimeout       string `json:"verify_timeout"`
	ThreadTranscript    bool   `json:"thread_transcript"`
	Scoring             bool   `json:"scoring"`
	ScoreDataset        string `json:"score_dataset"`
	ScoreWorkers        int    `json:"score_workers"`
	CustomPython        bool   `json:"custom_python"`
	EnvFile             bool   `json:"env_file"`
	CustomStateDB       bool   `json:"custom_state_db"`
	ResetRequested      bool   `json:"reset_requested"`
	AllowRemoteResetURL bool   `json:"allow_remote_reset_url"`
	KeepWorktrees       bool   `json:"keep_worktrees"`
	CloneCache          bool   `json:"clone_cache"`
	TranscriptCapture   bool   `json:"transcript_capture"`
}

// Marshal records the ordered, filtered task definitions, expanded driver names,
// requested non-secret settings, and this executable's available build metadata.
// TasksSHA256 hashes encoding/json's compact encoding of the tasks array, including
// order and every Spec field. Capture completes before callers start task execution.
func Marshal(cfg evalconfig.Config, specs []task.Spec, drivers []string) ([]byte, error) {
	tasksJSON, err := json.Marshal(specs)
	if err != nil {
		return nil, fmt.Errorf("encode selected tasks: %w", err)
	}
	z := cfg.Zarlcode
	m := runManifest{
		FormatVersion: 1,
		Build:         currentBuild(),
		Tasks:         specs,
		TasksSHA256:   fmt.Sprintf("%x", sha256.Sum256(tasksJSON)),
		Drivers:       drivers,
		Requested: requestedSettings{
			Languages: cfg.Input.Languages, Sample: cfg.Input.Sample, Ablations: cfg.Input.AblationSpec,
			Provider: z.Provider, Model: z.Model, CodexEffort: z.CodexEffort,
			TaskTimeout: cfg.Execution.TaskTimeout.String(), Concurrency: cfg.Execution.Concurrency,
			MaxIterations: z.MaxIter, ToolConcurrency: z.ToolConcurrency, ContextWindow: z.ContextWindow,
			StreamIdle: z.StreamIdle.String(), IterationTimeout: z.IterationTimeout.String(), DeadlineGrace: z.DeadlineGrace.String(),
			VerifiedAttempts: z.VerifiedAttempts, VerifyWorkers: z.VerifyWorkers, VerifyTimeout: z.VerifyTimeout.String(),
			ThreadTranscript: z.ThreadTranscript, Scoring: cfg.Scoring.Enabled,
			ScoreDataset: cfg.Scoring.Dataset, ScoreWorkers: cfg.Scoring.Workers, CustomPython: cfg.Scoring.Python != "",
			EnvFile: z.EnvFile != "", CustomStateDB: z.StateDB != "", ResetRequested: z.LlamacppResetURL != "",
			AllowRemoteResetURL: z.AllowRemoteResetURL, KeepWorktrees: cfg.Worktrees.Keep,
			CloneCache: cfg.Worktrees.CloneCache != "", TranscriptCapture: z.TranscriptDir != "",
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode run manifest: %w", err)
	}
	return data, nil
}

func currentBuild() buildIdentity {
	b := buildIdentity{Version: version.String(), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, Modules: []moduleIdentity{}}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			b.Revision = setting.Value
		case "vcs.time":
			b.RevisionAt = setting.Value
		case "vcs.modified":
			b.Modified = new(setting.Value == "true")
		}
	}
	modules := append([]*debug.Module{&info.Main}, info.Deps...)
	for _, mod := range modules {
		m := moduleIdentity{Path: mod.Path, Version: mod.Version, Sum: mod.Sum}
		if r := mod.Replace; r != nil {
			m.Replacement = &replacement{Local: r.Version == ""}
			if r.Version != "" {
				m.Replacement.Path, m.Replacement.Version, m.Replacement.Sum = r.Path, r.Version, r.Sum
			}
		}
		b.Modules = append(b.Modules, m)
	}
	slices.SortFunc(b.Modules, func(a, b moduleIdentity) int { return cmp.Compare(a.Path, b.Path) })
	return b
}
