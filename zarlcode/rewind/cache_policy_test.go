package rewind_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestCachePinClosedReplayAndLegacyBytes(t *testing.T) {
	target := rewind.Target{Provider: "anthropic", Model: "sonnet"}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "hello"}}
	legacy, err := rewind.EncodeResume(1, messages, target, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy, []byte("cache_pin")) || bytes.Contains(legacy, []byte("cache_intent")) {
		t.Fatal("legacy envelope gained cache fields")
	}
	target.CachePin = rewind.SavedCachePolicy("anthropic", backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED})
	target.CacheIntent = rewind.SavedCacheIntent(backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED})
	data, err := rewind.EncodeResume(1, messages, target, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := rewind.DecodeResume(data)
	if err != nil || state.Target != target {
		t.Fatalf("pin round trip: %v %+v", err, state.Target)
	}
	for _, bad := range [][]byte{
		bytes.Replace(data, []byte(`"mode":"disabled"`), []byte(`"mode":"DISABLED"`), 1),
		bytes.Replace(data, []byte(`"mode":"disabled"`), []byte(`"mode":2`), 1),
		bytes.Replace(data, []byte(`"mode":"disabled"`), []byte(`"mode":"disabled","mode":"disabled"`), 1),
		bytes.Replace(data, []byte(`"mode":"disabled"`), []byte(`"mode":"disabled","unknown":true`), 1),
		bytes.Replace(data, []byte(`"mode":"disabled"`), []byte(`"mode":"disabled","static_ttl":"1h"`), 1),
	} {
		if bytes.Equal(data, bad) {
			t.Fatal("fixture did not mutate")
		}
		if _, err := rewind.DecodeResume(bad); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("admitted malformed pin %s: %v", bad, err)
		}
	}
	if !bytes.Contains(legacy, []byte(`"target":{"provider":"anthropic","model":"sonnet","window":0,"reserve":0,"plan_mode":false,"codex_effort":""}`)) {
		t.Fatal("legacy version-one target bytes changed")
	}
}
