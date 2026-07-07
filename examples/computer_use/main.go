// Command computer_use demonstrates the universal computer-use tool surface with
// an LLM-driven Wikipedia quiz. It fetches random Wikipedia summaries, serves a
// local multiple-choice quiz, observes the page through computer_observe, asks
// an LLM to choose the answer, then clicks it through computer_act.
//
// Run:
//
//	go run ./examples/computer_use -chrome /usr/bin/chromium-browser
//
// Requires Chrome or Chromium plus an LLM provider configured through the usual
// zkit environment variables, for example LLM_PROVIDER=openai and OPENAI_API_KEY.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zarldev/zarlmono/zkit/zlog"
)

type config struct {
	chromePath   string
	headless     bool
	pause        time.Duration
	providerName string
	modelName    string
	baseURL      string
}

func main() {
	os.Exit(run())
}

func run() int {
	cfg := parseFlags()

	if _, err := zlog.Setup(zlog.WithStdout(true)); err != nil {
		fmt.Fprintf(os.Stderr, "zlog: %v\n", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	provider, err := buildProvider(ctx, cfg.providerName, cfg.modelName, cfg.baseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llm:", err)
		return 1
	}

	summaries, err := fetchSummaries(ctx, questionCount)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wikipedia:", err)
		return 1
	}
	distractors, err := generateDistractors(ctx, provider, summaries)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distractors:", err)
		return 1
	}
	questions := buildQuiz(summaries, distractors)
	url, shutdown, err := serveQuizPage(questions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		return 1
	}
	defer shutdown()

	harness, err := newQuizHarness(ctx, cfg, provider)
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser:", err)
		return 1
	}
	defer harness.close()

	obs, err := harness.run(ctx, questions, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	slog.Info("quiz complete", "visible_text", obs.VisibleText)
	pauseBeforeExit(ctx, cfg.pause, cfg.headless)
	return 0
}

func parseFlags() config {
	chromePath := flag.String("chrome", os.Getenv("CHROME_BIN"), "Chrome/Chromium executable path; defaults to PATH discovery")
	headless := flag.Bool("headless", true, "run Chrome headless")
	pause := flag.Duration("pause", 0, "time to keep the browser open before exiting; defaults to 10s when -headless=false")
	providerName := flag.String("provider", envDefault("LLM_PROVIDER", "openai"), "LLM provider: openai, anthropic, gemini, deepseek, llamacpp, ollama")
	modelName := flag.String("model", os.Getenv("LLM_MODEL"), "model id; defaults to provider package default")
	baseURL := flag.String("base-url", os.Getenv("LLM_BASE_URL"), "optional provider base URL override")
	flag.Parse()
	return config{
		chromePath:   *chromePath,
		headless:     *headless,
		pause:        *pause,
		providerName: *providerName,
		modelName:    *modelName,
		baseURL:      *baseURL,
	}
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
