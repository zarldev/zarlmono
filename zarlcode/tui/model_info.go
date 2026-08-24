package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type modelInfoResolver struct {
	s *engine.Settings
}

func newModelInfoResolver(s *engine.Settings) *modelInfoResolver {
	return &modelInfoResolver{s: s}
}

func (r *modelInfoResolver) summary(provider, model string) string {
	if r == nil || r.s == nil || r.s.Registry == nil || provider == "" || model == "" {
		return ""
	}
	ctx := ""
	if cw := r.s.Registry.ResolveContextWindowCached(provider, model); cw > 0 {
		ctx = fmtTokens(cw)
	}
	caps := strings.Join(modelCapabilitiesBadges(r.s.Registry.ResolveCapabilitiesCached(provider, model)), ",")
	cost := ""
	if in, out, ok := r.s.Registry.ResolveCostCached(provider, model); ok {
		cost = fmtCostPerM(in, out)
	}
	return palette.Muted.On(fmt.Sprintf("%6s  %-18s  %15s", ctx, caps, cost))
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		if n%1_000_000 == 0 {
			return fmt.Sprintf("%dM", n/1_000_000)
		}
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		if n%1000 == 0 {
			return fmt.Sprintf("%dk", n/1000)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return strconv.Itoa(n)
	}
}

func fmtCostPerM(inPer1k, outPer1k float64) string {
	in := inPer1k * 1000
	out := outPer1k * 1000
	if in == out {
		return "$" + fmtPrice(in) + "/M"
	}
	return "$" + fmtPrice(in) + "/" + fmtPrice(out) + "/M"
}

func fmtPrice(v float64) string {
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return fmt.Sprintf("%.1f", v)
	case v >= 1:
		return fmt.Sprintf("%.2f", v)
	case v > 0:
		return fmt.Sprintf("%.3f", v)
	default:
		return "0"
	}
}

func modelCapabilitiesBadges(c llm.ModelCapabilities) []string {
	var parts []string
	if c.SupportsTools {
		parts = append(parts, "tools")
	}
	if c.SupportsThinking {
		parts = append(parts, "think")
	}
	if c.SupportsVision {
		parts = append(parts, "vision")
	}
	return parts
}
