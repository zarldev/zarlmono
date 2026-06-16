package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// withEnvFile must make the .env visible to fn and restore the prior
// process environment afterwards — both for vars that already existed
// (restored to old value) and ones it introduced (unset again).
func TestWithEnvFile_AppliesThenRestores(t *testing.T) {
	const preexisting = "SWEBENCH_TEST_PREEXISTING"
	const introduced = "SWEBENCH_TEST_INTRODUCED"
	t.Setenv(preexisting, "original")
	os.Unsetenv(introduced)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(preexisting+"=overridden\n"+introduced+"=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var sawPre, sawIntro string
	if err := withEnvFile(envPath, func() {
		sawPre = os.Getenv(preexisting)
		sawIntro = os.Getenv(introduced)
	}); err != nil {
		t.Fatalf("withEnvFile: %v", err)
	}

	if sawPre != "overridden" || sawIntro != "new" {
		t.Errorf("inside fn: %s=%q %s=%q; want overridden/new", preexisting, sawPre, introduced, sawIntro)
	}
	if got := os.Getenv(preexisting); got != "original" {
		t.Errorf("after: %s=%q; want restored to original", preexisting, got)
	}
	if _, ok := os.LookupEnv(introduced); ok {
		t.Errorf("after: %s should be unset again", introduced)
	}
}

func TestWithEnvFile_ReadErrorIsReturned(t *testing.T) {
	if err := withEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"), func() {}); err == nil {
		t.Error("withEnvFile on a missing path should return an error, not swallow it")
	}
}
