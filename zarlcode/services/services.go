package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

const (
	SearxngURL = "http://127.0.0.1:8080"

	composeAsset  = "assets/docker-compose.yml"
	settingsAsset = "assets/searxng/settings.yml"
	limiterAsset  = "assets/searxng/limiter.toml"
)

var assetTargets = map[string]string{
	composeAsset:  "docker-compose.yml",
	settingsAsset: filepath.Join("searxng", "settings.yml"),
	limiterAsset:  filepath.Join("searxng", "limiter.toml"),
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
	URL       string
	Error     string
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
	st.Docker = dockerAvailable(ctx)
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SearxngURL+"/search?q=zarlcode&format=json", nil)
	if err == nil {
		if resp, err := client.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			st.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.Contains(string(body), "results")
			_ = resp.Body.Close()
		} else if st.Error == "" {
			st.Error = err.Error()
		}
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
	return runCompose(ctx, stdout, stderr, "up", "-d", "searxng")
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
	composeArgs := append([]string{"compose", "-f", file}, args...)
	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
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

func dockerAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
