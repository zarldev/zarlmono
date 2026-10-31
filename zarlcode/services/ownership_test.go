package services_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/services"
)

func TestMaterialiseUpgradesExactPreviousManifest(t *testing.T) {
	const previous = `services:
  searxng:
    image: searxng/searxng@sha256:3602e6ddbeba037f5d800d1ed9d296a8b93c9f5b3cf9d05fa179d0e766dd59a1
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./searxng/settings.yml:/etc/searxng/settings.yml:ro
    restart: unless-stopped
`
	dir := t.TempDir()
	if _, err := services.MaterialiseDir(t.Context(), dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	const customSettings = "custom settings must remain untouched\n"
	settings := filepath.Join(dir, "searxng", "settings.yml")
	if err := os.WriteFile(settings, []byte(customSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := services.MaterialiseDir(t.Context(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	checkPaths(t, result.Created, []string{"docker-compose.yml"})
	checkPaths(t, result.Skipped, []string{"searxng/settings.yml"})
	got, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := services.Assets.ReadFile("assets/docker-compose.yml")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("previous manifest was not upgraded to the current bundle: %v", err)
	}
	got, err = os.ReadFile(settings)
	if err != nil || string(got) != customSettings {
		t.Fatalf("custom settings changed: %v", err)
	}
}

func TestRecordedRuntimeDoesNotFallBack(t *testing.T) {
	for _, owner := range []string{"podman", "invalid"} {
		t.Run(owner, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			// Docker is available, but is not authorized to take over the recorded owner.
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			dir, err := services.Dir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "compose-runtime"), []byte(owner+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err = services.Stop(t.Context(), io.Discard, io.Discard)
			if err == nil || (owner == "podman" && !strings.Contains(err.Error(), "recorded podman compose unavailable")) {
				t.Fatalf("owner %q was silently replaced: %v", owner, err)
			}
		})
	}
}
