package cli

import (
	"fmt"
	"os"
	"strings"
)

// ResolvePrompt loads the task prompt from whichever source the
// invocation provided: --prompt-text wins when set, --prompt-file is
// the SWE-bench-shaped form. Returning ("", nil) means the user
// asked for no prompt — bail with the bad-invocation exit code.
func ResolvePrompt(promptFile, promptText string) (string, error) {
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
