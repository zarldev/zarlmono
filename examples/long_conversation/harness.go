package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

const systemPrompt = `You are a code researcher. Your job is to read source files and compile documentation.

The codebase has large files with verbose content. Your task:
1. List all files
2. Read each file and extract key information
3. Push documentation for each file using push_docs
4. You may need to read files in sequence as the context grows
5. After compaction, resume with the key findings and continue pushing docs`

// RunLongConversation demonstrates compaction during a long research task.
// The agent reads multiple verbose files; context pressure triggers compaction.
func RunLongConversation(ctx context.Context, client runner.Client, fs *FileSystem, rc *ResearchContext, maxAttempts int, extra ...options.Option[runner.Runner]) pursue.Outcome {
	// Build tools
	reg := tools.NewRegistry()
	var docsWritten []string
	reg.Register(&readFileTool{fs: fs, rc: rc})
	reg.Register(&listFilesTool{fs: fs, rc: rc})
	reg.Register(&pushDocsTool{rc: rc, docsWritten: &docsWritten})

	// Use the structural compactor - trims verbose content without model calls
	compactor := compact.NewStructural()

	// Create runner with compactor. extra opts let a caller (e.g. a test
	// observing compaction via a sink) tune the runner without duplicating
	// this wiring.
	opts := append([]options.Option[runner.Runner]{
		runner.WithTools(reg),
		runner.WithPromptText(systemPrompt),
		runner.WithCompactor(compactor),
	}, extra...)
	r := runner.New(client, opts...)

	// Goal: all 3 files documented
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if len(docsWritten) >= 3 {
			return pursue.Done()
		}
		return pursue.Retry(fmt.Sprintf("Only %d/3 documents published. Continue researching and pushing documentation.", len(docsWritten)))
	})

	return pursue.Drive(ctx, pursue.NewRequest(r.Run,
		runner.TaskSpec{ID: "long-conversation", Prompt: "Research the codebase and produce documentation for every file. Use read_file to examine each file, then push_docs to publish findings."},
		pursue.WithGoal(goal),
	),
		pursue.WithMaxAttempts(maxAttempts),
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report))
		}),
	)
}
