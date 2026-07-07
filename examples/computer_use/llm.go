package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func buildProvider(ctx context.Context, providerName, modelName, baseURL string) (llm.Provider, error) {
	reg := backends.NewRegistry()
	return reg.BuildWithConfig(ctx, providerName, backends.BuildConfig{Model: modelName, BaseURL: baseURL})
}

func askLLM(ctx context.Context, provider llm.Provider, visibleText string, choices []string) (string, error) {
	prompt := fmt.Sprintf(`You are answering a multiple-choice quiz generated from Wikipedia random article summaries.
Read the visible page text and choose exactly one of the answer choices.

Visible page text:
%s

Choices:
%s

Reply with only the exact answer choice text.`, visibleText, strings.Join(choices, "\n"))
	seq, err := provider.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Answer with exactly one provided choice and no extra text."},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   64,
		Temperature: 0,
	})
	slog.Debug("asking llm", "choices", choices)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for chunk, err := range seq {
		if err != nil {
			return "", err
		}
		b.WriteString(chunk.Content)
	}
	raw := b.String()
	slog.Debug("llm response", "raw", raw, "choices", choices)
	return matchChoice(raw, choices)
}

func matchChoice(raw string, choices []string) (string, error) {
	clean := strings.TrimSpace(strings.Trim(raw, "`\"'"))
	for _, choice := range choices {
		if strings.EqualFold(clean, choice) || strings.Contains(strings.ToLower(clean), strings.ToLower(choice)) {
			return choice, nil
		}
	}
	return "", fmt.Errorf("model returned %q, want one of %v", raw, choices)
}

func buttonNames(targets []model.TargetDescriptor) []string {
	var out []string
	for _, target := range targets {
		if target.Role == "button" && target.Name != "" {
			out = append(out, target.Name)
		}
	}
	return out
}

type distractorEntry struct {
	Title   string   `json:"title"`
	Choices []string `json:"choices"`
}

func generateDistractors(ctx context.Context, provider llm.Provider, summaries []wikiSummary) (map[string][]string, error) {
	if len(summaries) == 0 {
		return map[string][]string{}, nil
	}
	var parts []string
	for _, s := range summaries {
		parts = append(parts, fmt.Sprintf("Title: %s\nSummary: %s", s.Title, normalize(s.Extract)))
	}
	prompt := fmt.Sprintf(`For each Wikipedia article below, generate exactly 3 plausible but incorrect answer choices
that could realistically be confused with the correct title. The distractors
should be related topics, similar names, or common misconceptions.

Return ONLY a JSON array with no extra text:
[
  {"title": "Article Title Here", "choices": ["Distractor A", "Distractor B", "Distractor C"]},
  ...
]

Articles:
%s`, strings.Join(parts, "\n\n"))

	seq, err := provider.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Output only valid JSON arrays. No markdown, no explanation."},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   2048,
		Temperature: 0.7,
	})
	slog.Debug("generating distractors", "articles", len(summaries))
	if err != nil {
		return nil, fmt.Errorf("generate distractors: %w", err)
	}

	var b strings.Builder
	for chunk, err := range seq {
		if err != nil {
			return nil, err
		}
		b.WriteString(chunk.Content)
	}
	raw := b.String()
	slog.Debug("distractor response", "raw", raw)

	var entries []distractorEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parse distractors: %w\nraw: %s", err, raw)
	}

	out := make(map[string][]string, len(entries))
	for _, e := range entries {
		if len(e.Choices) != 3 {
			slog.Warn("distractor entry has wrong choice count", "title", e.Title, "count", len(e.Choices))
		}
		if len(e.Choices) > 0 {
			out[e.Title] = e.Choices
		}
	}
	if len(out) < len(summaries) {
		slog.Warn("distractors missing for some articles", "expected", len(summaries), "got", len(out))
	}
	return out, nil
}
