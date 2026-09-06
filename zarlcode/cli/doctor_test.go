package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/cli"
)

func TestRunDoctorDoesNotInitializeFreshHome(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)

	before := directoryEntries(t, userHome)
	var stdout, stderr bytes.Buffer
	if code := cli.RunDoctor(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("RunDoctor() = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	after := directoryEntries(t, userHome)
	if before != after {
		t.Fatalf("doctor mutated fresh home\nbefore: %q\nafter:  %q", before, after)
	}

	output := stdout.String()
	for _, want := range []string{
		"mode: offline, read-only",
		"WARN  home: not initialized",
		"run `zarlcode init`",
		"OK    state database: not initialized",
		"OK    credential vault: not configured",
		"1 warning",
		"0 failures",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q\n%s", want, output)
		}
	}
}

func TestRunDoctorReportsInitializedStateWithoutReadingSecrets(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	zarlHome := filepath.Join(userHome, ".zarlcode")
	for _, name := range []string{"skills", "tools", "hooks"} {
		if err := os.MkdirAll(filepath.Join(zarlHome, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const canary = "doctor-must-not-print-this-secret"
	writeDoctorFixture(t, filepath.Join(zarlHome, "state.db"), canary)
	writeDoctorFixture(t, filepath.Join(zarlHome, "master.kdf"), canary)

	var stdout, stderr bytes.Buffer
	if code := cli.RunDoctor(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("RunDoctor() = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, canary) {
		t.Fatalf("doctor exposed file content %q\n%s", canary, output)
	}
	for _, want := range []string{
		"OK    home:",
		"OK    skills:",
		"OK    tools:",
		"OK    hooks:",
		"OK    state database:",
		"is present and readable",
		"OK    credential vault: passphrase material is present and readable",
		"summary: 8 ok, 0 warnings, 0 failures",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q\n%s", want, output)
		}
	}
}

func TestRunDoctorFailsForInvalidExistingState(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	zarlHome := filepath.Join(userHome, ".zarlcode")
	for _, name := range []string{"skills", "hooks"} {
		if err := os.MkdirAll(filepath.Join(zarlHome, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeDoctorFixture(t, filepath.Join(zarlHome, "tools"), "not a directory")
	if err := os.Mkdir(filepath.Join(zarlHome, "state.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(zarlHome, "master.kdf"), 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := cli.RunDoctor(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("RunDoctor() = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"FAIL  tools:",
		"is not a directory",
		"FAIL  state database:",
		"is not a regular file",
		"FAIL  credential vault:",
		"3 failures",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q\n%s", want, output)
		}
	}
}

func TestRunDoctorFailsForDanglingSymlinks(t *testing.T) {
	t.Run("home", func(t *testing.T) {
		userHome := t.TempDir()
		t.Setenv("HOME", userHome)
		createDanglingSymlink(t, filepath.Join(userHome, ".zarlcode"))

		var stdout, stderr bytes.Buffer
		if code := cli.RunDoctor(nil, &stdout, &stderr); code != 1 {
			t.Fatalf("RunDoctor() = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "FAIL  home:") {
			t.Fatalf("dangling home not reported as failure\n%s", stdout.String())
		}
	})

	t.Run("extension database and vault", func(t *testing.T) {
		userHome := t.TempDir()
		t.Setenv("HOME", userHome)
		zarlHome := filepath.Join(userHome, ".zarlcode")
		for _, name := range []string{"tools", "hooks"} {
			if err := os.MkdirAll(filepath.Join(zarlHome, name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"skills", "state.db", "master.kdf"} {
			createDanglingSymlink(t, filepath.Join(zarlHome, name))
		}

		var stdout, stderr bytes.Buffer
		if code := cli.RunDoctor(nil, &stdout, &stderr); code != 1 {
			t.Fatalf("RunDoctor() = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}
		output := stdout.String()
		for _, want := range []string{"FAIL  skills:", "FAIL  state database:", "FAIL  credential vault:"} {
			if !strings.Contains(output, want) {
				t.Errorf("output missing %q\n%s", want, output)
			}
		}
	})
}

func TestRunDoctorValidatesArguments(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.RunDoctor([]string{arg}, &stdout, &stderr); code != 0 {
				t.Fatalf("RunDoctor() = %d, want 0\n%s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage: zarlcode doctor") {
				t.Fatalf("help output missing usage\n%s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("help stderr = %q, want empty", stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if code := cli.RunDoctor([]string{"unexpected"}, &stdout, &stderr); code != 4 {
		t.Fatalf("RunDoctor() = %d, want usage exit 4", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("usage stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{"doctor: no arguments accepted", "Usage: zarlcode doctor"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("usage stderr missing %q\n%s", want, stderr.String())
		}
	}
}

func createDanglingSymlink(t *testing.T, path string) {
	t.Helper()
	if err := os.Symlink("missing-target", path); err != nil {
		t.Skipf("create dangling symlink: %v", err)
	}
}

func writeDoctorFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func directoryEntries(t *testing.T, path string) string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join(names, "\n")
}
