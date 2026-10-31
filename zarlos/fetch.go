package main

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func maxLineWidth(s string) int {
	m := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " ")
		if n := len(line); n > m {
			m = n
		}
	}
	return m
}

func termCols() int {
	if c, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && c > 0 {
		return c
	}
	if out, err := exec.Command("tput", "cols").Output(); err == nil {
		if c, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && c > 0 {
			return c
		}
	}
	return 120
}

func centeredPad(width, total int) int {
	p := (total - width) / 2
	if p < 0 {
		return 0
	}
	return p
}

func logoWidth() int {
	b, err := os.ReadFile(homeDir() + "/.config/fastfetch/zarlcode.logo")
	if err != nil {
		return 91
	}
	return maxLineWidth(stripANSI(string(b)))
}

func infoWidth() int {
	out, err := exec.Command("fastfetch", "--logo", "none", "--pipe").Output()
	if err != nil {
		return 60
	}
	return maxLineWidth(stripANSI(string(out)))
}

// cmdFetch runs fastfetch with the logo and info block centered for the
// current terminal width.
func cmdFetch(args []string) {
	cols := termCols()
	argv := []string{
		"--logo-padding-left", strconv.Itoa(centeredPad(logoWidth(), cols)),
		"--key-padding-left", strconv.Itoa(centeredPad(infoWidth(), cols)),
	}
	argv = append(argv, args...)

	cmd := exec.Command("fastfetch", argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
}
