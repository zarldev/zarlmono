package service_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestSplitCompleteSentences(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		flushFinal bool
		wantSents  []string
		wantRemain string
	}{
		{
			name:       "empty",
			input:      "",
			flushFinal: false,
			wantSents:  nil,
			wantRemain: "",
		},
		{
			name:       "single complete sentence",
			input:      "Hello world. ",
			flushFinal: false,
			wantSents:  []string{"Hello world."},
			wantRemain: "",
		},
		{
			name:       "period without trailing whitespace is held as remainder",
			input:      "Hello world.",
			flushFinal: false,
			wantSents:  nil,
			wantRemain: "Hello world.",
		},
		{
			name:       "two complete sentences plus partial",
			input:      "Hi. How are you? I'm fine but",
			flushFinal: false,
			wantSents:  []string{"Hi.", "How are you?"},
			wantRemain: "I'm fine but",
		},
		{
			name:       "final flush treats remainder as a sentence",
			input:      "Hi. Trailing without period",
			flushFinal: true,
			wantSents:  []string{"Hi.", "Trailing without period"},
			wantRemain: "",
		},
		{
			name:       "exclamation and question",
			input:      "Wow! Really? ",
			flushFinal: false,
			wantSents:  []string{"Wow!", "Really?"},
			wantRemain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sents, rem := service.SplitCompleteSentences(tt.input, tt.flushFinal)
			if !equalStrings(sents, tt.wantSents) {
				t.Errorf("sentences = %q, want %q", sents, tt.wantSents)
			}
			if rem != tt.wantRemain {
				t.Errorf("remainder = %q, want %q", rem, tt.wantRemain)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
