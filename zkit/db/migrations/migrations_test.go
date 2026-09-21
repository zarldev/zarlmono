package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

// The child process isolates Goose's irreversible global registrations from
// other tests and repeated runs of this test.
func TestProviderIgnoresGlobalMigrations(t *testing.T) {
	if os.Getenv("ZKIT_TEST_GLOBAL_MIGRATIONS") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestProviderIgnoresGlobalMigrations$", "-test.count=1")
		cmd.Env = append(os.Environ(), "ZKIT_TEST_GLOBAL_MIGRATIONS=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated provider test: %v\n%s", err, output)
		}
		return
	}
	foreign := func(context.Context, *sql.Tx) error {
		return errors.New("foreign migration must not run")
	}
	goose.AddNamedMigrationContext("00031_foreign.go", foreign, nil)
	goose.AddNamedMigrationContext("90000_foreign.go", foreign, nil)
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	provider, err := migrations.NewProvider(d)
	if err != nil {
		t.Fatalf("foreign registration conflicted: %v", err)
	}
	for _, source := range provider.ListSources() {
		if source.Version == 90000 {
			t.Fatal("foreign migration entered state catalogue")
		}
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(t.Context())
	if err != nil || version != 34 {
		t.Fatalf("catalogue version = %d: %v", version, err)
	}
}
