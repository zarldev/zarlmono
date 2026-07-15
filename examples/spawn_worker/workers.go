package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Worker agent names
const (
	WorkerResearcher tools.ToolName = "researcher"
	WorkerReviewer   tools.ToolName = "reviewer"
	WorkerCoder      tools.ToolName = "coder"
)

// Worker prompts
const (
	ResearcherPrompt = `You are a code researcher. Your job is to explore the codebase and understand how things work.

You have read-only access: you can read files and list the project structure, but you CANNOT modify files.

When done, provide a concise summary of:
1. What files are relevant to the task
2. How the current implementation works
3. What changes would be needed

Be specific with file names and line counts.`

	ReviewerPrompt = `You are a code reviewer. Your job is to review plans and verify correctness.

You can read files and run tests, but you CANNOT modify source files.

Evaluate the proposed changes and provide:
1. Whether the plan is sound (yes/no with reasoning)
2. Any risks or concerns
3. Specific suggestions for improvement

Be thorough but concise.`

	CoderPrompt = `You are a coder. Your job is to implement changes to the codebase.

You have full access: read files, write new files, and edit existing files.

Implement the required changes:
1. Create new files when needed
2. Modify existing files carefully
3. Follow the established patterns in the codebase

After each change, verify the file was written correctly.`
)

// BuildWorkerRegistry creates a registry with tools appropriate for the worker's mode.
// The mode determines which tools are gated.
func BuildWorkerRegistry(fs *FileSystem, mode spawn.SpawnMode) *tools.Registry {
	reg := tools.NewRegistry()

	// All workers can read and list
	_ = reg.Register(&readFileTool{fs: fs})
	_ = reg.Register(&listFilesTool{fs: fs})

	// Only implement mode gets write/edit
	if mode == spawn.SpawnModeImplement {
		_ = reg.Register(&writeFileTool{fs: fs})
		_ = reg.Register(&editFileTool{fs: fs})
	}

	return reg
}

// BuildAgentResolver returns a resolver that creates workers with different configurations.
// This is used by the spawn tool to route to the appropriate worker.
func BuildAgentResolver(fs *FileSystem, client runner.Client) spawn.AgentResolver {
	return func(name string) (*runner.Runner, error) {
		switch name {
		case string(WorkerResearcher):
			// Researcher: explore mode (read-only)
			return runner.New(
				client,
				runner.WithTools(BuildWorkerRegistry(fs, spawn.SpawnModeExplore)),
				runner.WithPromptText(ResearcherPrompt),
			), nil

		case string(WorkerReviewer):
			// Reviewer: verify mode (read + test, but no edits)
			return runner.New(
				client,
				runner.WithTools(BuildWorkerRegistry(fs, spawn.SpawnModeVerify)),
				runner.WithPromptText(ReviewerPrompt),
			), nil

		case string(WorkerCoder):
			// Coder: implement mode (full access)
			return runner.New(
				client,
				runner.WithTools(BuildWorkerRegistry(fs, spawn.SpawnModeImplement)),
				runner.WithPromptText(CoderPrompt),
			), nil

		default:
			// Unknown agent - return nil to use parent
			return nil, nil
		}
	}
}

// ModePolicy returns the tool policy that enforces mode restrictions.
// This is the critical piece that blocks mutating tools in explore/verify modes.
func ModePolicy() func(spawn.SpawnMode, tools.ToolSpec) bool {
	return func(mode spawn.SpawnMode, spec tools.ToolSpec) bool {
		switch mode {
		case spawn.SpawnModeExplore:
			// Read-only: block all mutating tools
			return !spec.Mutates

		case spawn.SpawnModeVerify:
			// Can read and test, but not edit source files
			return !spec.Mutates

		case spawn.SpawnModeImplement:
			// Full access
			return true

		default:
			// Unknown mode: conservative - allow all
			return true
		}
	}
}
