package repeatcap_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/repeatcap"
)

func TestCounter_RefusesPastMax(t *testing.T) {
	t.Parallel()
	var c repeatcap.Counter
	for i := 1; i <= 3; i++ {
		hits, over := c.HitsAndCheck("rootA", "list_skills", 3)
		if over {
			t.Fatalf("call %d should be under cap, got over=true (hits=%d)", i, hits)
		}
	}
	hits, over := c.HitsAndCheck("rootA", "list_skills", 3)
	if !over {
		t.Fatalf("call 4 should be over cap, got over=false (hits=%d)", hits)
	}
}

func TestCounter_ScopesByRootAndTool(t *testing.T) {
	t.Parallel()
	var c repeatcap.Counter
	c.HitsAndCheck("rootA", "list_skills", 3)
	c.HitsAndCheck("rootA", "list_skills", 3)
	c.HitsAndCheck("rootA", "list_skills", 3)
	if _, over := c.HitsAndCheck("rootB", "list_skills", 3); over {
		t.Errorf("different root should have its own counter; got over=true")
	}
	if _, over := c.HitsAndCheck("rootA", "list_agents", 3); over {
		t.Errorf("different tool name should have its own counter; got over=true")
	}
}

func TestCounter_MaxZeroDisablesCap(t *testing.T) {
	t.Parallel()
	var c repeatcap.Counter
	for i := range 100 {
		if _, over := c.HitsAndCheck("rootC", "list_skills", 0); over {
			t.Fatalf("max=0 should never trip; tripped at call %d", i+1)
		}
	}
}
