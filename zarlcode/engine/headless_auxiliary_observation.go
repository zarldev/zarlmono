package engine

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// HeadlessAuxiliarySnapshot projects latest-only optional evidence, never a
// history or accounting total. Both slots are null when no helper was observed.
type HeadlessAuxiliarySnapshot struct {
	Judge        *HeadlessAuxiliaryObservation `json:"judge"`
	SpawnPlanner *HeadlessAuxiliaryObservation `json:"spawn_planner"`
}

// HeadlessAuxiliaryObservation projects admitted helper evidence using the
// existing usage and execution DTOs. StreamOutcome and ResponseValid describe
// independent transport and semantic outcomes; no raw response or error is kept.
type HeadlessAuxiliaryObservation struct {
	TaskID                 taskscope.ID          `json:"task_id"`
	Depth                  int                   `json:"depth"`
	Provider               string                `json:"provider"`
	Model                  string                `json:"model"`
	Duration               time.Duration         `json:"duration"`
	StreamOutcome          string                `json:"stream_outcome"`
	ResponseValid          bool                  `json:"response_valid"`
	LegacyUsage            *HeadlessUsage        `json:"legacy_usage"`
	NormalizedPricingUsage *HeadlessPricingUsage `json:"normalized_pricing_usage"`
	Execution              *HeadlessExecution    `json:"execution"`
}

func projectAuxiliarySnapshot(s AuxiliarySnapshot) HeadlessAuxiliarySnapshot {
	return HeadlessAuxiliarySnapshot{Judge: projectAuxiliaryObservation(s.Judge), SpawnPlanner: projectAuxiliaryObservation(s.SpawnPlanner)}
}

func projectAuxiliaryObservation(o *AuxiliaryObservation) *HeadlessAuxiliaryObservation {
	if o == nil {
		return nil
	}
	i := o.Inference
	return &HeadlessAuxiliaryObservation{
		TaskID: i.TaskID, Depth: i.Depth, Provider: i.Provider, Model: i.Model,
		Duration: i.Duration, StreamOutcome: i.Outcome.String(), ResponseValid: o.ResponseValid,
		LegacyUsage: projectLegacyUsage(i.Usage), NormalizedPricingUsage: projectPricingUsage(i.PricingUsage), Execution: projectExecution(i.Execution),
	}
}
