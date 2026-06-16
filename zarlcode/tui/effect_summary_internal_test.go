package tui

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestEffectSummaries_FileEffects(t *testing.T) {
	e := tools.NewFileEffect(tools.FileRename, "new.go")
	e.File.FromPath = "old.go"
	got := effectSummaries([]tools.Effect{e})
	want := []string{"renamed old.go → new.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("effectSummaries = %v, want %v", got, want)
	}
}

func TestEffectSummaries_MultipleFileEffects(t *testing.T) {
	got := effectSummaries([]tools.Effect{
		tools.NewFileEffect(tools.FileModify, "a.go"),
		tools.NewFileEffect(tools.FileCreate, "b.go"),
	})
	want := []string{"changed 2 files"}
	if !slices.Equal(got, want) {
		t.Fatalf("effectSummaries = %v, want %v", got, want)
	}
}

func TestEffectSummaries_ProcessEffects(t *testing.T) {
	e := tools.NewProcessEffect("go test ./...", 1)
	e.Process.OutputTruncated = true
	got := effectSummaries([]tools.Effect{e})
	want := []string{"exit 1, output truncated"}
	if !slices.Equal(got, want) {
		t.Fatalf("effectSummaries = %v, want %v", got, want)
	}
}
