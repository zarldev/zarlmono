package toolkit_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
)

func ExampleTool() {
	type args struct {
		Text string `json:"text" doc:"text to echo"`
	}

	echo := toolkit.Tool[args, string]{
		Name:        tools.ToolName("echo_text"),
		Description: "echo the provided text",
		Func: func(_ context.Context, a args) (string, error) {
			return a.Text, nil
		},
	}

	spec := echo.Describe()
	out, err := echo.Call(context.Background(), json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		panic(err)
	}

	fmt.Println(spec.Name)
	fmt.Println(out)
	// Output:
	// echo_text
	// hello
}
