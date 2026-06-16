package zapp_test

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/zapp"
)

type exampleProgram struct{}

func (exampleProgram) Name() string { return "example" }

func (exampleProgram) Create(_ context.Context, app *zapp.App[string]) (string, error) {
	_ = app.AddCloser("cleanup", zapp.CloseFunc(func() error {
		fmt.Println("cleanup")
		return nil
	}))
	return "ready", nil
}

func (exampleProgram) Run(_ context.Context, _ *zapp.App[string], value string) int {
	fmt.Println(value)
	return zapp.ExitOK
}

func ExampleApp() {
	code := zapp.New[string](exampleProgram{}).Run(context.Background())
	fmt.Println(code)
	// Output:
	// ready
	// cleanup
	// 0
}
