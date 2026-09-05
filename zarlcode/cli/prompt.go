package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ResolvePrompt loads a headless task prompt from either an inline value or a
// file. Supplying both sources is an invocation error; an empty source resolves
// to an empty prompt.
func ResolvePrompt(promptFile, promptText string) (string, error) {
	if promptFile != "" && strings.TrimSpace(promptText) != "" {
		return "", errors.New("prompt-file and prompt-text are mutually exclusive")
	}
	if strings.TrimSpace(promptText) != "" {
		return promptText, nil
	}
	if promptFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt-file %q: %w", promptFile, err)
	}
	return string(data), nil
}
