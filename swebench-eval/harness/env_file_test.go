package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func TestZarlcodeDriverEnvFileReadErrorIsReturned(t *testing.T) {
	driver := &harness.ZarlcodeDriver{EnvFile: filepath.Join(t.TempDir(), "does-not-exist.env")}
	result := driver.Run(t.Context(), harness.Task{})
	if result.Err == nil {
		t.Fatal("Run with a missing EnvFile should return an error")
	}
}

func TestZarlcodeDriverEnvFileRestoresEnvironment(t *testing.T) {
	const preexisting = "SWEBENCH_TEST_PREEXISTING"
	const introduced = "SWEBENCH_TEST_INTRODUCED"
	t.Setenv(preexisting, "original")
	if err := os.Unsetenv(introduced); err != nil {
		t.Fatal(err)
	}

	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte(preexisting+"=overridden\n"+introduced+"=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	driver := &harness.ZarlcodeDriver{EnvFile: envPath, Provider: "not-a-provider"}
	_ = driver.Run(t.Context(), harness.Task{})
	driver.Close()

	if got := os.Getenv(preexisting); got != "original" {
		t.Errorf("after Run: %s=%q; want restored to original", preexisting, got)
	}
	if _, ok := os.LookupEnv(introduced); ok {
		t.Errorf("after Run: %s should be unset again", introduced)
	}
}
