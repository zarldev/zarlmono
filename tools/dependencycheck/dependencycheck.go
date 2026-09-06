// Package dependencycheck verifies dependency versions across repository modules.
package dependencycheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	anthropicModule   = "github.com/anthropics/anthropic-sdk-go"
	anthropicVersion  = "v1.68.0"
	jsonschemaModule  = "github.com/invopop/jsonschema"
	jsonschemaVersion = "v0.14.0"
	commandTimeout    = 30 * time.Second
	waitDelay         = 2 * time.Second
)

// ErrIncompatible reports that at least one module resolved a version outside the validated pair.
var ErrIncompatible = errors.New("incompatible Anthropic/JSON Schema dependency pair")

// Run resolves and verifies the validated dependency pair in every owned consumer module.
func Run(ctx context.Context, root string, stdout, stderr io.Writer) error {
	modules := []string{"examples", "swebench-eval", "zarlcode", "zkit"}
	incompatible := false
	for _, module := range modules {
		anthropic, err := moduleVersion(ctx, filepath.Join(root, module), anthropicModule)
		if err != nil {
			return fmt.Errorf("%s: resolve %s: %w", module, anthropicModule, err)
		}
		jsonschema, err := moduleVersion(ctx, filepath.Join(root, module), jsonschemaModule)
		if err != nil {
			return fmt.Errorf("%s: resolve %s: %w", module, jsonschemaModule, err)
		}
		if anthropic != anthropicVersion || jsonschema != jsonschemaVersion {
			fmt.Fprintf(stderr, "%s: incompatible Anthropic/JSON Schema pair: %s %s, %s %s (want %s and %s)\n",
				module, anthropicModule, anthropic, jsonschemaModule, jsonschema, anthropicVersion, jsonschemaVersion)
			incompatible = true
		}
	}
	if incompatible {
		return ErrIncompatible
	}
	fmt.Fprintln(stdout, "Anthropic/JSON Schema pair verified in: examples swebench-eval zarlcode zkit")
	return nil
}

func moduleVersion(ctx context.Context, dir, module string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "go", "list", "-mod=readonly", "-m", "-f", "{{.Version}}", module)
	cmd.Dir = dir
	cmd.Env = environmentWithout("GOWORK")
	cmd.Env = append(cmd.Env, "GOWORK=off")
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if commandCtx.Err() != nil {
			return "", commandCtx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("go list: %w: %s", err, detail)
		}
		return "", fmt.Errorf("go list: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func environmentWithout(key string) []string {
	prefix := key + "="
	environment := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, prefix) {
			environment = append(environment, value)
		}
	}
	return environment
}
