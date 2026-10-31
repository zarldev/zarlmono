package rewind_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
)

func TestGoogleReplayHistoricalTextProjectionAndEmptyArguments(t *testing.T) {
	for _, args := range []string{``, `,"args":null`, `,"args":{}`} {
		t.Run(args, func(t *testing.T) {
			messages := googleReplayHistory()
			messages[0].Content = "calling tools"
			messages[0].ToolCalls[0].Function.Arguments = "null"
			if args == `,"args":{}` {
				messages[0].ToolCalls[0].Function.Arguments = "{}"
			}
			messages[0].ContinuationItems[0].Data = []byte(`{"functionCall":{"name":"read"` + args + `},"thoughtSignature":"c2ln"}`)
			if err := rewind.ValidateLegacyContext(messages, "gemini"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
