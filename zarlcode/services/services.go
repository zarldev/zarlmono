package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

const (
	SearxngURL = "http://127.0.0.1:8080"

	composeAsset  = "assets/docker-compose.yml"
	settingsAsset = "assets/searxng/settings.yml"
)

// Exact previously shipped manifest: only this known bundle is auto-upgraded.
// Arbitrary user edits still require an explicit force operation.
const previousComposeManifest = `services:
  searxng:
    image: searxng/searxng@sha256:3602e6ddbeba037f5d800d1ed9d296a8b93c9f5b3cf9d05fa179d0e766dd59a1
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./searxng/settings.yml:/etc/searxng/settings.yml:ro
    restart: unless-stopped
`

var assetTargets = map[string]string{
	composeAsset:  "docker-compose.yml",
	settingsAsset: filepath.Join("searxng", "settings.yml"),
}

type MaterialiseResult struct {
	Dir     string
	Created []string
	Existed []string
	Skipped []string
}

type Status struct {
	Dir       string
	Installed bool
	Healthy   bool
	Docker    bool
	// Runtime names the recorded owner, or the available command before first start.
	Runtime string
	URL     string
	Error   string
}

func Dir() (string, error) {
	h, err := home.HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "services"), nil
}

func ComposeFile() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "docker-compose.yml"), nil
}

func Materialise(ctx context.Context, force bool) (MaterialiseResult, error) {
	dir, err := Dir()
	if err != nil {
		return MaterialiseResult{}, err
	}
	return MaterialiseDir(ctx, dir, force)
}

func MaterialiseDir(_ context.Context, dir string, force bool) (MaterialiseResult, error) {
	res := MaterialiseResult{Dir: dir}
	for asset, rel := range assetTargets {
		data, err := Assets.ReadFile(asset)
		if err != nil {
			return res, err
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return res, err
		}
		if cur, err := os.ReadFile(dst); err == nil {
			switch {
			case bytes.Equal(cur, data):
				res.Existed = append(res.Existed, rel)
				continue
			case asset == composeAsset && bytes.Equal(cur, []byte(previousComposeManifest)):
				// Upgrade only the unmodified previous bundle's SELinux mount policy.
			case !force:
				res.Skipped = append(res.Skipped, rel)
				continue
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return res, err
		}
		res.Created = append(res.Created, rel)
	}
	return res, nil
}

func Probe(ctx context.Context) Status {
	dir, err := Dir()
	st := Status{URL: SearxngURL}
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Dir = dir
	st.Installed = installedAt(dir)
	runtime, runtimeErr := serviceCompose(ctx, dir)
	st.Runtime = runtime.label
	st.Docker = runtime.label == "docker compose"
	healthErr := CheckSearch(ctx, SearxngURL)
	st.Healthy = healthErr == nil
	if err := errors.Join(runtimeErr, healthErr); err != nil {
		st.Error = err.Error()
	}
	return st
}

func Start(ctx context.Context, stdout, stderr io.Writer) error {
	if _, err := Materialise(ctx, false); err != nil {
		return err
	}
	if st := Probe(ctx); st.Healthy {
		_, _ = fmt.Fprintf(stdout, "SearXNG already reachable at %s; leaving existing service in place.\n", st.URL)
		return nil
	}
	if err := runCompose(ctx, stdout, stderr, "up", "-d", "searxng"); err != nil {
		return err
	}
	return waitForSearch(ctx)
}

func Stop(ctx context.Context, stdout, stderr io.Writer) error {
	return runCompose(ctx, stdout, stderr, "down")
}

func Logs(ctx context.Context, stdout, stderr io.Writer) error {
	return runCompose(ctx, stdout, stderr, "logs", "--tail=80", "searxng")
}

func runCompose(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	file, err := ComposeFile()
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	runtime, err := serviceCompose(ctx, dir)
	if err != nil {
		return err
	}
	if args[0] == "up" {
		// Record before starting: even a partially failed start must stop via its owner.
		if err := os.WriteFile(filepath.Join(dir, runtimeOwnerFile), []byte(runtime.binary+"\n"), 0o600); err != nil {
			return fmt.Errorf("record service runtime owner: %w", err)
		}
	}
	composeArgs := append(append([]string(nil), runtime.prefix...), "-f", file)
	composeArgs = append(composeArgs, args...)
	cmd := newComposeCommand(ctx, runtime.binary, composeArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = errors.Join(err, cause)
		}
		return fmt.Errorf("%s %s: %w", runtime.label, strings.Join(args, " "), err)
	}
	return nil
}

func installedAt(dir string) bool {
	for _, rel := range assetTargets {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			return false
		}
	}
	return true
}
