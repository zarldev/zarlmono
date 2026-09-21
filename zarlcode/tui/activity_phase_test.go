package tui_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestActivityPhaseParsing(t *testing.T) {
	var zero tui.ActivityPhase
	if zero.IsValid() {
		t.Fatal("zero activity phase must be invalid")
	}
	if _, err := tui.ParseActivityPhase(0); !errors.Is(err, tui.ErrParseActivityPhase) {
		t.Fatalf("parse zero = %v, want ErrParseActivityPhase", err)
	}
	number := 1
	for phase := range tui.ActivityPhases.All() {
		for _, input := range []any{number, phase.String()} {
			parsed, err := tui.ParseActivityPhase(input)
			if err != nil || parsed != phase {
				t.Errorf("parse %v = %v, %v; want %v", input, parsed, err, phase)
			}
		}
		data, err := phase.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var restored tui.ActivityPhase
		if err := restored.UnmarshalJSON(data); err != nil || restored != phase {
			t.Errorf("JSON round trip %v = %v, %v", phase, restored, err)
		}
		number++
	}
}
