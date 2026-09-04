package services_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zarlcode/services"
)

var expectedAssets = map[string]string{
	"docker-compose.yml":   "assets/docker-compose.yml",
	"searxng/settings.yml": "assets/searxng/settings.yml",
}

func TestMaterialiseDir(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()

	created, err := services.MaterialiseDir(ctx, dir, false)
	if err != nil {
		t.Fatalf("materialise new directory: %v", err)
	}
	checkPaths(t, created.Created, assetPaths())
	checkPaths(t, created.Existed, nil)
	checkPaths(t, created.Skipped, nil)
	checkMaterialisedAssets(t, dir)
	checkComposePolicy(t, dir)
	checkNoLimiter(t, dir)

	unchanged, err := services.MaterialiseDir(ctx, dir, false)
	if err != nil {
		t.Fatalf("materialise unchanged directory: %v", err)
	}
	checkPaths(t, unchanged.Created, nil)
	checkPaths(t, unchanged.Existed, assetPaths())
	checkPaths(t, unchanged.Skipped, nil)

	composeFile := filepath.Join(dir, "docker-compose.yml")
	customCompose := []byte("custom compose\n")
	if err := os.WriteFile(composeFile, customCompose, 0o600); err != nil {
		t.Fatalf("write custom compose file: %v", err)
	}
	preserved, err := services.MaterialiseDir(ctx, dir, false)
	if err != nil {
		t.Fatalf("materialise without force: %v", err)
	}
	checkPaths(t, preserved.Created, nil)
	checkPaths(t, preserved.Existed, []string{"searxng/settings.yml"})
	checkPaths(t, preserved.Skipped, []string{"docker-compose.yml"})
	got, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read preserved compose file: %v", err)
	}
	if !bytes.Equal(got, customCompose) {
		t.Fatalf("compose file was not preserved: got %q, want %q", got, customCompose)
	}

	replaced, err := services.MaterialiseDir(ctx, dir, true)
	if err != nil {
		t.Fatalf("materialise with force: %v", err)
	}
	checkPaths(t, replaced.Created, []string{"docker-compose.yml"})
	checkPaths(t, replaced.Existed, []string{"searxng/settings.yml"})
	checkPaths(t, replaced.Skipped, nil)
	checkMaterialisedAssets(t, dir)
	checkNoLimiter(t, dir)
}

func checkMaterialisedAssets(t *testing.T, dir string) {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk materialised files: %v", err)
	}
	checkPaths(t, files, assetPaths())

	for rel, asset := range expectedAssets {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read materialised %s: %v", rel, err)
		}
		want, err := services.Assets.ReadFile(asset)
		if err != nil {
			t.Fatalf("read embedded %s: %v", asset, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("materialised %s differs from embedded asset", rel)
		}
	}
}

func checkComposePolicy(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	compose := string(data)
	if !strings.Contains(compose, `"127.0.0.1:8080:8080"`) {
		t.Error("compose file does not bind SearXNG to loopback")
	}
	if strings.Contains(compose, "container_name:") {
		t.Error("compose file defines a global container name")
	}
	if strings.Contains(compose, "limiter.toml") {
		t.Error("compose file mounts limiter.toml")
	}
	const image = "searxng/searxng@sha256:3602e6ddbeba037f5d800d1ed9d296a8b93c9f5b3cf9d05fa179d0e766dd59a1"
	if !strings.Contains(compose, image) {
		t.Errorf("compose file does not use pinned image %s", image)
	}
}

func checkNoLimiter(t *testing.T, dir string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "searxng", "limiter.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("limiter.toml exists or could not be checked: %v", err)
	}
}

func assetPaths() []string {
	paths := make([]string, 0, len(expectedAssets))
	for path := range expectedAssets {
		paths = append(paths, path)
	}
	return paths
}

func checkPaths(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("paths mismatch (-want +got):\n%s", diff)
	}
}
