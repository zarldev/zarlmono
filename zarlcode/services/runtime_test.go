package services_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/services"
)

func TestCheckSearch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		issue  string
	}{
		{name: "empty results", status: 200, body: `{"results":[]}`},
		{name: "results", status: 200, body: `{"results":[{"title":"Go","url":"https://go.dev"}]}`},
		{name: "HTML mentioning results", status: 200, body: `<html>results</html>`, issue: "decode"},
		{name: "missing results", status: 200, body: `{}`, issue: "results array"},
		{name: "null results", status: 200, body: `{"results":null}`, issue: "results array"},
		{name: "object results", status: 200, body: `{"results":{}}`, issue: "results array"},
		{name: "trailing junk", status: 200, body: `{"results":[]} junk`, issue: "decode"},
		{name: "JSON disabled", status: 403, body: `forbidden`, issue: "search.formats"},
		{name: "upstream error", status: 503, body: `unavailable`, issue: "503"},
		{name: "oversized", status: 200, body: strings.Repeat(" ", (1<<20)+1), issue: "exceeds 1 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
					t.Errorf("unexpected search request: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			err := services.CheckSearch(t.Context(), server.URL)
			if tc.issue == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.issue) {
				t.Fatalf("health error = %v, want diagnostic containing %q", err, tc.issue)
			}
		})
	}
}

func TestCheckSearchCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := services.CheckSearch(ctx, "http://127.0.0.1:1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled health check = %v", err)
	}
}

func TestComposeLifecycleRuntimeSelection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		dockerVersion string
		podmanVersion string
		owner         string
		wantRuntime   string
	}{
		{name: "Docker preferred", dockerVersion: "0", podmanVersion: "0", wantRuntime: "docker"},
		{name: "Podman only", podmanVersion: "0", wantRuntime: "podman"},
		{name: "broken Docker falls back", dockerVersion: "1", podmanVersion: "0", wantRuntime: "podman"},
		{name: "Podman owner survives Docker installation", dockerVersion: "0", podmanVersion: "0", owner: "podman", wantRuntime: "podman"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", bin)
			if tc.owner != "" {
				dir, err := services.Dir()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "compose-runtime"), []byte(tc.owner+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, version := range map[string]string{"docker": tc.dockerVersion, "podman": tc.podmanVersion} {
				if version == "" {
					continue
				}
				// Fixed test scripts emulate only the trusted runtime CLI boundary.
				script := "#!/bin/sh\nif [ \"$2\" = version ]; then exit " + version + "; fi\nprintf '" + name + "\\n'\nprintf '<%s>\\n' \"$@\"\n"
				if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			file, err := services.ComposeFile()
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range []struct {
				name string
				run  func(context.Context, io.Writer, io.Writer) error
				args string
			}{
				{name: "stop", run: services.Stop, args: "<down>\n"},
				{name: "logs", run: services.Logs, args: "<logs>\n<--tail=80>\n<searxng>\n"},
			} {
				t.Run(op.name, func(t *testing.T) {
					var output bytes.Buffer
					if err := op.run(t.Context(), &output, &output); err != nil {
						t.Fatal(err)
					}
					want := tc.wantRuntime + "\n<compose>\n<-f>\n<" + file + ">\n" + op.args
					if output.String() != want {
						t.Fatalf("runtime invocation = %q, want %q", output.String(), want)
					}
				})
			}
		})
	}
}

func TestComposeMissingRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	err := services.Stop(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Compose provider") {
		t.Fatalf("missing runtime error = %v", err)
	}
}

func TestComposeCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := services.Stop(ctx, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Compose operation = %v", err)
	}
}

func TestComposeRunningOperationCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := "#!/bin/sh\nif [ \"$2\" = version ]; then exit 0; fi\nprintf 'running\\n'\nwhile :; do :; done\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := services.Logs(ctx, cancelOnWrite{cancel: cancel}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("running Compose cancellation = %v", err)
	}
}

type cancelOnWrite struct{ cancel context.CancelFunc }

func (w cancelOnWrite) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}
