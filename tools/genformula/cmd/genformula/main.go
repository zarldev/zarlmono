// Command genformula renders the zarlcode Homebrew formula from release
// checksums. It accepts release metadata and writes a formula:
//
//	go run ./tools/genformula/cmd/genformula \
//	  --version v0.1.2 \
//	  --checksums artifacts/checksums.txt \
//	  --output homebrew/zarlcode.rb
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/tools/genformula"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genformula:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "release version, e.g. v0.1.2")
	checksums := flag.String("checksums", "", "path to the checksums file")
	output := flag.String("output", "", "path to write the rendered formula")
	flag.Parse()

	switch {
	case *version == "":
		return errors.New("--version is required")
	case *checksums == "":
		return errors.New("--checksums is required")
	case *output == "":
		return errors.New("--output is required")
	}

	f, err := os.Open(*checksums)
	if err != nil {
		return fmt.Errorf("open checksums: %w", err)
	}
	defer f.Close()

	sums, err := genformula.ParseChecksums(f)
	if err != nil {
		return fmt.Errorf("parse %s: %w", *checksums, err)
	}

	rendered, err := genformula.Render(*version, sums)
	if err != nil {
		return err
	}

	if dir := filepath.Dir(*output); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}
	if err := os.WriteFile(*output, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	return nil
}
