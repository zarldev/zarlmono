// Command hnupvote is a deterministic harness around an LLM loop: it
// drives a real Chrome (via chromedp) to upvote the top post on Hacker
// News, logging in if a login wall appears, and re-driving the LLM until
// a programmatic oracle confirms the vote registered.
//
// The "deterministic" part is the harness contract, not the model: the
// require_auth guardrail gates the upvote on being logged in, and the
// harness only declares success when the session verifies the upvote
// against the page — the model cannot finish on a hallucinated "done".
//
// It performs a REAL action on a live site, so it is gated behind
// -confirm. Requires Chrome installed. Configured via .env (see
// .env.example); see buildClient for how LLM_PROVIDER selects the model
// backend. The openai-codex backend reuses zarlcode's vault, so no LLM
// secret goes in .env.
//
// Run: go run ./examples/hnupvote -confirm
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/joho/godotenv"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Quiet the runner's per-iteration INFO logs so the demo's own
	// progress (attempt summaries + final status) is what you see.
	slog.SetLogLoggerLevel(slog.LevelWarn)

	// Load .env (best-effort, non-overriding) so credentials don't have
	// to be exported. Works whether you run from the repo root
	// (go run ./examples/hnupvote) or from inside the example dir.
	for _, p := range []string{".env", "examples/hnupvote/.env"} {
		_ = godotenv.Load(p)
	}

	confirm := flag.Bool("confirm", false, "perform the real upvote on live Hacker News")
	headless := flag.Bool("headless", true, "run Chrome headless")
	attempts := flag.Int("attempts", 4, "max harness re-drive attempts")
	flag.Parse()

	if !*confirm {
		fmt.Fprintln(os.Stderr, "hnupvote performs a real action on live Hacker News; pass -confirm to proceed")
		return 2
	}

	ctx := context.Background()
	client, closeClient, err := buildClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llm:", err)
		return 1
	}
	defer closeClient()

	page, cancel, err := newChromedp(ctx, *headless)
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser:", err)
		return 1
	}
	defer cancel()

	sess := NewSession(page, Creds{User: os.Getenv("HN_USER"), Pass: os.Getenv("HN_PASS")})

	out := RunUpvote(ctx, client, sess, *attempts)
	if out.Err() != nil {
		fmt.Fprintln(os.Stderr, "harness:", out.Err())
		return 1
	}
	fmt.Fprintf(os.Stdout, "status=%s attempts=%d verified_upvoted=%v title=%q last_state=%q\n",
		out.Status(), out.Attempts, sess.VerifiedUpvoted(), sess.UpvotedTitle(), sess.LastState())
	if out.Status() != pursue.Statuses.SUCCEEDED {
		return 1
	}
	return 0
}
