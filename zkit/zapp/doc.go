// Package zapp provides a small lifecycle wrapper for command-line apps.
//
// A Program creates a typed app instance, runs it, and returns a process exit
// code. App handles signal cancellation, panic recovery, and deterministic
// cleanup of named io.Closer resources.
//
// Typical usage keeps main.go small:
//
//	type Zarlcode struct {
//		// config, stores, services, TUI handles, etc.
//	}
//
//	type CLI struct{}
//
//	func (CLI) Name() string { return "zarlcode" }
//
//	func (CLI) Create(ctx context.Context, app *zapp.App[*Zarlcode]) (*Zarlcode, error) {
//		z := &Zarlcode{}
//		// app.AddCloser("state-db", db)
//		return z, nil
//	}
//
//	func (CLI) Run(ctx context.Context, app *zapp.App[*Zarlcode], z *Zarlcode) int {
//		return zapp.ExitOK
//	}
//
//	func main() {
//		app := zapp.New[*Zarlcode](CLI{})
//		os.Exit(app.Run(context.Background()))
//	}
package zapp
