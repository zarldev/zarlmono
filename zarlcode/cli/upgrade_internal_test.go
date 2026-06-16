package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/prefs"
)

func makeUpgradeSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "zarlcode", "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zarlcode", "cmd", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskfile := "version: '3'\n\ntasks:\n  zarlcode:\n    cmds:\n      - echo installed\n"
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateUpgradeSource(t *testing.T) {
	good := makeUpgradeSource(t)
	if err := validateUpgradeSource(good); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	missingTaskfile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(missingTaskfile, "zarlcode", "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missingTaskfile, "zarlcode", "cmd", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeSource(missingTaskfile); err == nil {
		t.Fatal("missing Taskfile accepted")
	}

	missingTarget := makeUpgradeSource(t)
	if err := os.WriteFile(filepath.Join(missingTarget, "Taskfile.yml"), []byte("version: '3'\n\ntasks:\n  test:\n    cmds:\n      - go test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeSource(missingTarget); err == nil {
		t.Fatal("Taskfile without zarlcode task accepted")
	}

	missingMain := makeUpgradeSource(t)
	if err := os.Remove(filepath.Join(missingMain, "zarlcode", "cmd", "main.go")); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeSource(missingMain); err == nil {
		t.Fatal("source without zarlcode/cmd/main.go accepted")
	}
}

func TestUpgradeSettingsCommandsPersistGlobally(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	source := makeUpgradeSource(t)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	var out, errOut bytes.Buffer

	if code := runUpgradeWithService(ctx, svc, []string{"source", "set", source}, &out, &errOut, false); code != 0 {
		t.Fatalf("source set exit %d stderr=%q", code, errOut.String())
	}
	got, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource)
	if err != nil || !ok || got.Value != source {
		t.Fatalf("upgrade_source = %+v ok=%v err=%v, want %q", got, ok, err, source)
	}

	out.Reset()
	errOut.Reset()
	if code := runUpgradeWithService(ctx, svc, []string{"bin-path", "set", bin}, &out, &errOut, false); code != 0 {
		t.Fatalf("bin-path set exit %d stderr=%q", code, errOut.String())
	}
	got, ok, err = svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath)
	if err != nil || !ok || got.Value != bin {
		t.Fatalf("upgrade_bin_path = %+v ok=%v err=%v, want %q", got, ok, err, bin)
	}

	for _, tc := range []struct {
		args []string
		key  string
		want string
	}{
		{[]string{"restart", "set", "true"}, settingKeyUpgradeRestart, "true"},
		{[]string{"dry-run", "set", "true"}, settingKeyUpgradeDryRun, "true"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runUpgradeWithService(ctx, svc, tc.args, &out, &errOut, false); code != 0 {
			t.Fatalf("%v exit %d stderr=%q", tc.args, code, errOut.String())
		}
		got, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, tc.key)
		if err != nil || !ok || got.Value != tc.want {
			t.Fatalf("%s = %+v ok=%v err=%v, want %q", tc.key, got, ok, err, tc.want)
		}
	}

	out.Reset()
	errOut.Reset()
	if code := runUpgradeWithService(ctx, svc, []string{"source", "clear"}, &out, &errOut, false); code != 0 {
		t.Fatalf("source clear exit %d stderr=%q", code, errOut.String())
	}
	if _, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource); err != nil || ok {
		t.Fatalf("source clear left row ok=%v err=%v", ok, err)
	}
}

func TestRunUpgradeDryRunDoesNotExecuteTask(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	source := makeUpgradeSource(t)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	if err := svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, source); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin); err != nil {
		t.Fatal(err)
	}

	called := false
	oldRunner := runUpgradeCommand
	runUpgradeCommand = func(context.Context, string, io.Writer, io.Writer) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { runUpgradeCommand = oldRunner })

	res, err := runUpgrade(ctx, svc, upgradeOptions{DryRun: true, DryRunOverride: true})
	if err != nil {
		t.Fatalf("runUpgrade dry-run: %v", err)
	}
	if called {
		t.Fatal("dry-run executed task")
	}
	if !res.DryRun || res.Source != source || res.BinPath != bin || strings.Join(res.Command, " ") != "go tool task zarlcode" {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
}

func TestRunUpgradeExecutesTaskInConfiguredSource(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	source := makeUpgradeSource(t)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, source)
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	oldRunner := runUpgradeCommand
	var gotDir string
	runUpgradeCommand = func(_ context.Context, dir string, stdout, stderr io.Writer) (string, error) {
		gotDir = dir
		_, _ = stdout.Write([]byte("built\n"))
		return "built\n", nil
	}
	t.Cleanup(func() { runUpgradeCommand = oldRunner })

	var out bytes.Buffer
	res, err := runUpgrade(ctx, svc, upgradeOptions{Stdout: &out})
	if err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	if gotDir != source {
		t.Fatalf("task dir = %q, want %q", gotDir, source)
	}
	if res.Output != "built\n" || out.String() != "built\n" {
		t.Fatalf("output result=%q stdout=%q", res.Output, out.String())
	}
}

func TestUpgradeRestartExecsPersistedBinary(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	source := makeUpgradeSource(t)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, source)
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	oldRunner := runUpgradeCommand
	runUpgradeCommand = func(context.Context, string, io.Writer, io.Writer) (string, error) { return "", nil }
	t.Cleanup(func() { runUpgradeCommand = oldRunner })

	oldExec := execUpgradeBinary
	var execPath string
	execUpgradeBinary = func(path string, argv, env []string) error {
		execPath = path
		return nil
	}
	t.Cleanup(func() { execUpgradeBinary = oldExec })

	var out, errOut bytes.Buffer
	code := runUpgradeWithService(ctx, svc, []string{"--restart"}, &out, &errOut, true)
	if code != 0 {
		t.Fatalf("upgrade --restart exit %d stderr=%q", code, errOut.String())
	}
	if execPath != bin {
		t.Fatalf("exec path = %q, want %q", execPath, bin)
	}
}

func TestRunUpgradeReturnsTaskErrorWithOutput(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	source := makeUpgradeSource(t)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, source)
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	oldRunner := runUpgradeCommand
	runUpgradeCommand = func(context.Context, string, io.Writer, io.Writer) (string, error) {
		return "boom\n", errors.New("task failed")
	}
	t.Cleanup(func() { runUpgradeCommand = oldRunner })

	res, err := runUpgrade(ctx, svc, upgradeOptions{})
	if err == nil {
		t.Fatal("expected task error")
	}
	if res.Output != "boom\n" {
		t.Fatalf("output = %q, want boom", res.Output)
	}
}

func TestResolveUpgradeBinPathFollowsSymlink(t *testing.T) {
	// Create a fake binary in a "real" directory.
	realDir := t.TempDir()
	realBin := filepath.Join(realDir, "zarlcode")
	if err := os.WriteFile(realBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to the fake binary.
	linkDir := t.TempDir()
	linkBin := filepath.Join(linkDir, "zarlcode")
	if err := os.Symlink(realBin, linkBin); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// Validate that a symlink path is accepted by validateUpgradeBinPath.
	if err := validateUpgradeBinPath(linkBin); err != nil {
		t.Fatalf("validateUpgradeBinPath(%q) = %v", linkBin, err)
	}

	// Ensure that the symlink resolves to the real path.
	resolved, err := filepath.EvalSymlinks(linkBin)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) = %v", linkBin, err)
	}
	if resolved != realBin {
		t.Fatalf("EvalSymlinks(%q) = %q, want %q", linkBin, resolved, realBin)
	}
}
