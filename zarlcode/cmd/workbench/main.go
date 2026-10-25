//go:build desktop

// Command workbench opens the native coding-agent frontend for the cwd.
package main

import (
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zarlcode/desktop"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

func main() {
	sandbox.ExecShim()
	if err := desktop.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
