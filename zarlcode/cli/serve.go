package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// llamaServerBinaryEnv lets the user point `zarlcode serve` at
// a custom llama-server binary (e.g. a local fork compiled from
// ~/src/llamacpp-tuning/). Falls back to "llama-server" on $PATH.
const llamaServerBinaryEnv = "LLAMA_SERVER_BIN"

// defaultHF is the unsloth MTP-tuned Qwen3.6-35B model the user
// runs by default. -hf auto-downloads the GGUF from Hugging Face on
// first launch; subsequent launches reuse the cached file.
const defaultHF = "unsloth/Qwen3.6-35B-A3B-MTP-GGUF"

// canonicalServeArgs is the llama-server invocation that matches
// zarlcode's defaults: serves on 8081 (the default
// LLAMACPP_BASE_URL — 8080 is reserved for the bundled SearXNG
// container), MTP speculative decoding per the canonical baseline
// (--spec-type mtp -np 1 --spec-draft-n-max 2 per memory:
// project_qwen36_mtp_baseline), Jinja for OpenAI-compatible tool
// calling, and all layers offloaded to GPU.
//
// Anything the user passes to `zarlcode serve` is appended after
// these — that's how you tune (more layers, different quant, host
// bind, log level, etc.) without rebuilding the binary.
func canonicalServeArgs() []string {
	return []string{
		"-hf", defaultHF,
		"--port", "8081",
		"--host", "127.0.0.1",
		"--jinja",
		"--spec-type", "mtp",
		"-np", "1",
		"--spec-draft-n-max", "2",
		"-ngl", "999",
	}
}

// RunServe is the entry point for `zarlcode serve`.
// It exec-replaces this process with llama-server using the canonical
// arguments plus any extras the caller supplied. Ctrl-C in the
// terminal goes straight to llama-server.
//
// We don't manage llama-server's lifecycle beyond exec'ing it — no
// supervision, no restart loop, no log redirection. The user keeps
// the same tuning workflow they had before; zarlcode just
// removes the "what was that command again?" step.
func RunServe(args []string, stdout io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printServeHelp(stdout)
		return 0
	}

	binName := os.Getenv(llamaServerBinaryEnv)
	if binName == "" {
		binName = "llama-server"
	}
	binPath, err := exec.LookPath(binName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zarlcode serve: %q not found on $PATH (set %s to override)\n",
			binName, llamaServerBinaryEnv)
		fmt.Fprintln(os.Stderr, "  build llama.cpp:  https://github.com/ggerganov/llama.cpp")
		return 1
	}

	final := append([]string{binPath}, canonicalServeArgs()...)
	final = append(final, args...)

	fmt.Fprintf(stdout, "zarlcode serve -> %s\n", strings.Join(final, " "))
	fmt.Fprintln(stdout, "  (Ctrl-C stops it. Edit args after `zarlcode serve` to tune.)")

	// Exec-replace so signals and TTY ownership behave as if the
	// user ran llama-server directly. zarlcode's pid goes away.
	if err := syscall.Exec(binPath, final, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec:", err)
		return 1
	}
	return 0 // unreachable
}

func printServeHelp(w io.Writer) {
	fmt.Fprintln(w, `usage: zarlcode serve [llama-server args...]

Launches the canonical llama-server config for the default model:
    `+defaultHF+`

The canonical args are appended first; anything you pass on the
command line follows, so you can override or add flags:

    zarlcode serve              # default MTP setup
    zarlcode serve -ngl 60      # offload only 60 layers
    zarlcode serve --port 9091  # different port

Override the binary path with `+llamaServerBinaryEnv+` (e.g. to
point at a local llama.cpp fork).`)
}
