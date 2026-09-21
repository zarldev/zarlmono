package task_test

import (
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func TestMaterializeRejectsUnsafeInstancePaths(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../sibling", "/absolute", "nested/child"} {
		t.Run(id, func(t *testing.T) {
			if _, err := task.Materialize(t.Context(), task.Spec{InstanceID: id}, t.TempDir(), ""); err == nil {
				t.Fatal("unsafe instance path accepted")
			}
		})
	}
}
