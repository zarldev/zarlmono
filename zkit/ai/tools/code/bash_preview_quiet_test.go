package code_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestBashPreviewFlushesAQuietBurstBeforeCommandExit(t *testing.T) {
	path, gate := bashPreviewGate(t)
	var first, second sync.Once
	command := fmt.Sprintf(`printf 'first-line\n'; read -r signal < %q; printf 'second-line\n'; read -r signal < %q; printf 'finished\n'`, path, path)
	result, _ := executeWithPreview(t, command, func(update tools.ToolOutputUpdate) {
		if strings.Contains(update.Text, "first-line") {
			first.Do(func() {
				if _, err := gate.WriteString("first\n"); err != nil {
					t.Error(err)
				}
			})
		}
		if strings.Contains(update.Text, "second-line") {
			second.Do(func() {
				if _, err := gate.WriteString("second\n"); err != nil {
					t.Error(err)
				}
			})
		}
	})
	if !strings.Contains(result.Data.(string), "finished\n") || !strings.Contains(result.Data.(string), "[exit 0]") {
		t.Fatalf("coalesced quiet output was withheld until completion: %s", result.Data)
	}
}
