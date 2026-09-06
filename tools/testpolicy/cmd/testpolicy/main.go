// Command testpolicy checks repository test-source policy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/tools/testpolicy"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	base := testpolicy.DefaultBase
	if flag.NArg() > 0 {
		base = flag.Arg(0)
	}
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: testpolicy [-root directory] [base]")
		os.Exit(2)
	}
	if err := testpolicy.Run(context.Background(), *root, base, os.Stderr); err != nil {
		switch {
		case errors.Is(err, testpolicy.ErrViolations):
			os.Exit(1)
		case errors.Is(err, testpolicy.ErrUnknownBase):
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		default:
			fmt.Fprintln(os.Stderr, "testpolicy:", err)
			os.Exit(1)
		}
	}
}
