// Package zarlcode is the entry layer for the zarlcode TUI: it parses
// argv, dispatches operational subcommands, and hands the interactive
// launch to the zapp lifecycle harness. The application itself lives in
// zarlcode/tui (services + bubbletea model); subcommands live in
// zarlcode/cli. The thin package main in zarlcode/cmd calls Main.
//
// Usage:
//
//	cd /path/to/project && go run ./zarlcode/cmd  # operate on the cwd
//	go run ./zarlcode/cmd -continue               # resume the last session
package zarlcode

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zarlcode/cli"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/version"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/zapp"
)

// Main is the entrypoint for zarlcode. Operational subcommands run
// before any flag parsing or TUI setup and never return to the launch
// path; the interactive launch is handed to the zapp harness, which
// owns signal handling, panic recovery, and deterministic shutdown.
func Main() {
	// Sandbox shim first — before subcommands, flags, anything. When this
	// process is the re-exec'd child of a sandboxed shell command,
	// ExecShim applies the kernel policy and execs the real command in
	// place of the TUI; in a normal launch it's a no-op.
	sandbox.ExecShim()

	// Subcommands short-circuit. --askpass is the sudo SUDO_ASKPASS shim
	// (returns to exit cleanly); the rest are operational helpers that
	// exit with their own status code.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--askpass":
			cli.RunAskpassClient(os.Args[2:])
			return
		case "init":
			os.Exit(cli.RunInit(os.Stdout))
		case "keys":
			os.Exit(cli.RunKeys(os.Args[2:], os.Stdout))
		case "upgrade":
			os.Exit(cli.RunUpgrade(os.Args[2:], os.Stdout))
		}
	}

	envFile := flag.String("env", "", "path to a .env file to load before reading config")
	agentName := flag.String("agent", "", "named agent to start with (loaded from agents/<name>.md)")
	resumeFlag := flag.Bool(
		"continue",
		false,
		"resume the previous session in this workspace (from ~/.zarlcode/state.db)",
	)
	versionFlag := flag.Bool("version", false, "print the build version and exit")
	headlessFlag := flag.Bool(
		"headless",
		false,
		"run one task to completion without a TUI; record result to state.db headless_runs and exit",
	)
	promptFile := flag.String(
		"prompt-file",
		"",
		"path to a file containing the task prompt (for --headless)",
	)
	promptText := flag.String(
		"prompt-text",
		"",
		"inline task prompt (for --headless; wins over --prompt-file when both set)",
	)
	maxIterFlag := flag.Int(
		"max-iter",
		0,
		"override MaxIterations for the headless task (0 = use config default)",
	)
	pprofAddr := flag.String(
		"pprof",
		"",
		"serve Go pprof and runtime metrics at this address (for example 127.0.0.1:6060)",
	)
	traceFile := flag.String(
		"trace",
		"",
		"write a Go execution trace to this file until zarlcode exits",
	)
	flag.Parse()

	if *versionFlag {
		fmt.Fprintln(os.Stdout, version.String())
		return
	}

	// Resolve the task prompt up front so usage errors exit 4 before any
	// service is wired. headless permits an empty prompt (the runner decides).
	var prompt string
	if *headlessFlag {
		p, perr := cli.ResolvePrompt(*promptFile, *promptText)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "prompt:", perr)
			os.Exit(4)
		}
		prompt = p
	}

	// Hand off to the zapp lifecycle harness: tui.Launch.Create wires the
	// services (registering closers), tui.Launch.Run drives the
	// TUI/headless task.
	os.Exit(zapp.New(tui.Launch{
		EnvFile:   *envFile,
		AgentName: *agentName,
		Resume:    *resumeFlag,
		Headless:  *headlessFlag,
		Prompt:    prompt,
		MaxIter:   *maxIterFlag,
		PprofAddr: *pprofAddr,
		TraceFile: *traceFile,
	}).Run(context.Background()))
}
