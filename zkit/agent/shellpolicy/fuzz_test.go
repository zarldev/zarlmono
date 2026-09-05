package shellpolicy_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

func FuzzUnixPolicyAnalysis(f *testing.F) {
	for _, seed := range []string{
		"",
		"go test ./...",
		"cd ..",
		"printf x > result.txt",
		"python3 -c 'print(1)'",
		"cat <<'EOF' | python3\nprint(1)\nEOF",
		"unterminated '",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, command string) {
		if len(command) > 32<<10 {
			t.Skip()
		}

		parser := shellpolicy.NewUnixParser()
		firstIR, firstErr := parser.Parse(command)
		secondIR, secondErr := parser.Parse(command)
		if !reflect.DeepEqual(firstIR, secondIR) {
			t.Fatalf("Parse is not deterministic: first=%#v second=%#v", firstIR, secondIR)
		}
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("Parse error presence is not deterministic: first=%v second=%v", firstErr, secondErr)
		}

		decision := shellpolicy.NewPolicyEngine().Decide(firstIR)
		if firstErr != nil && !decision.IsBlocked {
			t.Fatalf("syntax error did not fail closed: %v", firstErr)
		}

		firstTargets, firstTargetErr := shellpolicy.WriteTargets(command)
		secondTargets, secondTargetErr := shellpolicy.WriteTargets(command)
		if !reflect.DeepEqual(firstTargets, secondTargets) {
			t.Fatalf("WriteTargets is not deterministic: first=%q second=%q", firstTargets, secondTargets)
		}
		if (firstTargetErr == nil) != (secondTargetErr == nil) {
			t.Fatalf("WriteTargets error presence is not deterministic: first=%v second=%v", firstTargetErr, secondTargetErr)
		}

		firstMutation, firstMutationErr := shellpolicy.UnscopedMutationCommand(command)
		secondMutation, secondMutationErr := shellpolicy.UnscopedMutationCommand(command)
		if firstMutation != secondMutation {
			t.Fatalf("UnscopedMutationCommand is not deterministic: first=%q second=%q", firstMutation, secondMutation)
		}
		if (firstMutationErr == nil) != (secondMutationErr == nil) {
			t.Fatalf("UnscopedMutationCommand error presence is not deterministic: first=%v second=%v", firstMutationErr, secondMutationErr)
		}
	})
}
