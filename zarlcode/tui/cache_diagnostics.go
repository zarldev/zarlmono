package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	cacheDiagnosticsVersion = 1
	costAccountingVersion   = 1
)

const (
	billingClassMetered      = "metered"
	billingClassLocal        = "local"
	billingClassSubscription = "subscription"
)

// CacheTargetDiagnostics is an immutable snapshot of terminal usage attributed
// to one physical provider/model and billing class. KnownCostUSD is a base-rate
// estimate containing only fresh input/output components whose rates are known;
// cache charges and prompt-length tiers may make the estimate incomplete.
// Coverage counters named Tasks also include compactor settlements for wire
// compatibility. ReasoningTokens is a reported subtotal of CompletionTokens;
// ReasoningReportedSettlements records its coverage, not an extra token charge.
type CacheTargetDiagnostics struct {
	Provider                     string  `json:"provider"`
	Model                        string  `json:"model"`
	BillingClass                 string  `json:"billing_class"`
	SettledTasks                 int     `json:"settled_tasks"`
	CompactorCalls               int     `json:"compactor_calls,omitempty"`
	CompactorDurationNanos       int64   `json:"compactor_duration_nanos,omitempty"`
	RootTasks                    int     `json:"root_tasks,omitempty"`
	ChildTasks                   int     `json:"child_tasks,omitempty"`
	MissingUsageTasks            int     `json:"missing_usage_tasks,omitempty"`
	ReportedZeroUsageTasks       int     `json:"reported_zero_usage_tasks,omitempty"`
	PromptTokens                 int     `json:"prompt_tokens,omitempty"`
	CompletionTokens             int     `json:"completion_tokens,omitempty"`
	ReasoningTokens              int     `json:"reasoning_tokens,omitempty"`
	ReasoningReportedSettlements int     `json:"reasoning_reported_settlements,omitempty"`
	CacheReadTokens              int     `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens          int     `json:"cache_creation_tokens,omitempty"`
	CacheCreation5mTokens        int     `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens        int     `json:"cache_creation_1h_tokens,omitempty"`
	CacheCreationUnknownTTL      int     `json:"cache_creation_unknown_ttl,omitempty"`
	CacheReadShare               float64 `json:"cache_read_share,omitempty"`
	CacheReadShareKnown          bool    `json:"cache_read_share_known,omitempty"`
	KnownCostUSD                 float64 `json:"known_cost_usd,omitempty"`
	BaseRateUnknownTasks         int     `json:"base_rate_unknown_tasks,omitempty"`
	PricingTierUnknownTasks      int     `json:"pricing_tier_unknown_tasks,omitempty"`
	CacheReadRateUnknownTasks    int     `json:"cache_read_rate_unknown_tasks,omitempty"`
	CacheWriteRateUnknownTasks   int     `json:"cache_write_rate_unknown_tasks,omitempty"`
}

type cacheTargetKey struct {
	provider, model, billingClass string
}

func (p usagePrice) billingClass() string {
	switch {
	case p.local:
		return billingClassLocal
	case p.subscription:
		return billingClassSubscription
	default:
		return billingClassMetered
	}
}

func (s *RunState) foldCacheTarget(price usagePrice, u *llm.Usage, root, compactor bool) {
	if s.cacheTargets == nil {
		s.cacheTargets = make(map[cacheTargetKey]CacheTargetDiagnostics)
	}
	key := cacheTargetKey{provider: price.provider, model: price.model, billingClass: price.billingClass()}
	d := s.cacheTargets[key]
	d.Provider, d.Model, d.BillingClass = key.provider, key.model, key.billingClass
	if compactor {
		d.CompactorCalls++
	} else {
		d.SettledTasks++
		if root {
			d.RootTasks++
		} else {
			d.ChildTasks++
		}
	}
	if u == nil {
		d.MissingUsageTasks++
		s.cacheTargets[key] = d
		return
	}
	if usageReportedZero(u) {
		d.ReportedZeroUsageTasks++
	}
	d.PromptTokens += u.PromptTokens
	d.CompletionTokens += u.CompletionTokens
	if u.ReasoningTokensReported {
		d.ReasoningTokens += u.ReasoningTokens
		d.ReasoningReportedSettlements++
	}
	d.CacheReadTokens += u.CachedTokens
	d.CacheCreationTokens += u.CacheCreationTokens
	d.CacheCreation5mTokens += u.CacheCreation5mTokens
	d.CacheCreation1hTokens += u.CacheCreation1hTokens
	d.CacheCreationUnknownTTL = max(d.CacheCreationTokens-d.CacheCreation5mTokens-d.CacheCreation1hTokens, 0)
	if d.PromptTokens > 0 {
		d.CacheReadShare = float64(d.CacheReadTokens) / float64(d.PromptTokens)
		d.CacheReadShareKnown = true
	}
	if price.metered {
		if price.missingBaseRate(u) {
			d.BaseRateUnknownTasks++
		}
		d.KnownCostUSD += price.cost(u.PromptTokens, u.CachedTokens, u.CacheCreationTokens, u.CompletionTokens)
		if price.tierSensitive && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
			d.PricingTierUnknownTasks++
		}
		if u.CachedTokens > 0 {
			d.CacheReadRateUnknownTasks++
		}
		if u.CacheCreationTokens > 0 {
			d.CacheWriteRateUnknownTasks++
		}
	}
	s.cacheTargets[key] = d
}

func usageReportedZero(u *llm.Usage) bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.CachedTokens == 0 &&
		u.CacheCreationTokens == 0 && u.CacheCreation5mTokens == 0 && u.CacheCreation1hTokens == 0
}

// promptTierSensitive identifies models whose static base rates can be adjusted
// by per-request prompt length. Terminal aggregation lacks the per-request
// boundaries needed to reconstruct those tiers exactly.
func promptTierSensitive(model string) bool {
	switch strings.ToLower(model) {
	case "gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna":
		return true
	default:
		return false
	}
}

func cloneCacheTargets(in []CacheTargetDiagnostics) []CacheTargetDiagnostics {
	if len(in) == 0 {
		return nil
	}
	return slices.Clone(in)
}

func sortedCacheTargets(in map[cacheTargetKey]CacheTargetDiagnostics) []CacheTargetDiagnostics {
	if len(in) == 0 {
		return nil
	}
	out := make([]CacheTargetDiagnostics, 0, len(in))
	for _, d := range in {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b CacheTargetDiagnostics) int {
		if n := cmp.Compare(a.Provider, b.Provider); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Model, b.Model); n != 0 {
			return n
		}
		return cmp.Compare(a.BillingClass, b.BillingClass)
	})
	return out
}

func cacheTargetMap(in []CacheTargetDiagnostics) map[cacheTargetKey]CacheTargetDiagnostics {
	if len(in) == 0 {
		return nil
	}
	out := make(map[cacheTargetKey]CacheTargetDiagnostics, len(in))
	for _, d := range in {
		key := cacheTargetKey{provider: d.Provider, model: d.Model, billingClass: d.BillingClass}
		out[key] = d
	}
	return out
}
