package main

import (
	"os"
	"os/exec"
)

// cmdTheme re-applies the Night Instrument CLI theme.
func cmdTheme() {
	cmd := exec.Command(homeDir() + "/src/ricing/install-night-instrument-cli.sh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
}
