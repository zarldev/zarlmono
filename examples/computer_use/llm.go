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

type generatedQuestion struct {
	SourceTitle  string   `json:"source_title"`
	Question     string   `json:"question"`
	Answer       string   `json:"answer"`
	WrongAnswers []string `json:"wrong_answers"`
}

func generateQuizQuestions(ctx context.Context, provider llm.Provider, summaries []wikiSummary) ([]quizQuestion, error) {
	if len(summaries) == 0 {
		return nil, nil
	}
	var parts []string
	for _, s := range summaries {
		parts = append(parts, fmt.Sprintf("Source title: %s\nSummary: %s", s.Title, normalize(s.Extract)))
	}
	prompt := fmt.Sprintf(`For each Wikipedia source below, generate one multiple-choice quiz question.
The question must be answerable from general knowledge or from the source summary,
but MUST NOT reveal the answer text in the question itself.

For each source return:
- source_title: the exact source title provided
- question: a standalone question that does not contain the answer text
- answer: the correct answer
- wrong_answers: exactly 4 plausible but incorrect answers

Return ONLY a JSON array with no extra text:
[
  {"source_title":"Exact Source Title","question":"Question text?","answer":"Correct answer","wrong_answers":["Wrong A","Wrong B","Wrong C","Wrong D"]}
]

Sources:
%s`, strings.Join(parts, "\n\n"))

	slog.Debug("generating quiz questions", "articles", len(summaries))
	seq, err := provider.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Output only valid JSON arrays. No markdown, no explanation."},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   4096,
		Temperature: 0.7,
	})
	if err != nil {
		return nil, fmt.Errorf("generate quiz questions: %w", err)
	}

	var b strings.Builder
	for chunk, err := range seq {
		if err != nil {
			return nil, err
		}
		b.WriteString(chunk.Content)
	}
	raw := b.String()
	slog.Debug("quiz generation response", "raw", raw)

	var entries []generatedQuestion
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parse quiz questions: %w\nraw: %s", err, raw)
	}
	if len(entries) != len(summaries) {
		return nil, fmt.Errorf("generated %d questions, want %d", len(entries), len(summaries))
	}

	questions := make([]quizQuestion, 0, len(entries))
	for i, entry := range entries {
		question, err := entry.toQuizQuestion(i)
		if err != nil {
			return nil, err
		}
		questions = append(questions, question)
		slog.Debug("quiz question", "q", i+1, "source_title", entry.SourceTitle, "answer", entry.Answer, "choices", question.Choices)
	}
	slog.Info("generated quiz questions", "count", len(questions))
	return questions, nil
}

func (q generatedQuestion) toQuizQuestion(index int) (quizQuestion, error) {
	question := strings.TrimSpace(q.Question)
	answer := strings.TrimSpace(q.Answer)
	if question == "" || answer == "" {
		return quizQuestion{}, fmt.Errorf("generated question %q has empty question or answer", q.SourceTitle)
	}
	if strings.Contains(strings.ToLower(question), strings.ToLower(answer)) {
		return quizQuestion{}, fmt.Errorf("generated question for %q reveals answer %q", q.SourceTitle, answer)
	}

	seen := map[string]bool{strings.ToLower(answer): true}
	choices := []string{answer}
	for _, wrong := range q.WrongAnswers {
		wrong = strings.TrimSpace(wrong)
		key := strings.ToLower(wrong)
		if wrong == "" || seen[key] {
			continue
		}
		seen[key] = true
		choices = append(choices, wrong)
	}
	if len(choices) != 5 {
		return quizQuestion{}, fmt.Errorf("generated question for %q has %d unique choices, want 5", q.SourceTitle, len(choices))
	}
	return quizQuestion{
		Prompt:  question,
		Answer:  answer,
		Choices: rotate(choices, index%len(choices)),
	}, nil
}
