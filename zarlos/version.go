package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

// osVersion reads the zarlOS version, defaulting to 0.1.0.
func osVersion() string {
	b, err := os.ReadFile(filepath.Join(homeDir(), ".zarlos", "version"))
	if err != nil {
		return "0.1.0"
	}
	return strings.TrimSpace(string(b))
}

func cmdVersion() {
	fmt.Printf("%s %s\n", primary.On("zarlOS"), muted.On("v"+osVersion()))
}
