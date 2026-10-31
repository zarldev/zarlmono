package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func cmdHealth() int {
	fmt.Printf("%s %s health:\n", primary.On("zarlOS"), muted.On("v"+osVersion()))

	ok, fail := 0, 0
	report := func(good bool, name string) {
		if good {
			fmt.Printf("  %s %s\n", success.On("ok"), text.On(name))
			ok++
		} else {
			fmt.Printf("  %s %s\n", danger.On("MISSING"), text.On(name))
			fail++
		}
	}

	for _, t := range []string{"fastfetch", "bat", "delta", "fzf", "eza", "btop", "ghostty"} {
		_, err := exec.LookPath(t)
		report(err == nil, t)
	}

	report(hasNerdFont(), "JetBrainsMono Nerd Font Mono")

	if err := exec.Command("systemctl", "--user", "is-active", "--quiet", "noctalia-niri.service").Run(); err == nil {
		fmt.Printf("  %s %s (active)\n", success.On("ok"), text.On("noctalia-niri.service"))
		ok++
	} else {
		fmt.Printf("  %s %s\n", warn.On("inactive"), text.On("noctalia-niri.service"))
		fail++
	}

	for _, f := range []string{
		homeDir() + "/.config/fastfetch/config.jsonc",
		homeDir() + "/.config/bat/config",
		homeDir() + "/.config/delta/themes.gitconfig",
		homeDir() + "/.config/btop/btop.conf",
		homeDir() + "/.bashrc.d/night-instrument-cli.sh",
	} {
		_, err := os.Stat(f)
		report(err == nil, f)
	}

	fmt.Printf("  %d ok, %d problem(s)\n", ok, fail)
	if fail > 0 {
		return 1
	}
	return 0
}

func hasNerdFont() bool {
	out, err := exec.Command("fc-list").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "jetbrainsmono nerd font mono")
}
