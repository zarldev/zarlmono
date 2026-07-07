package computer_test

import (
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

func TestActionKindJSONAndParse(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(computer.ActionKinds.CLICK)
	if err != nil {
		t.Fatalf("json.Marshal(ActionKinds.CLICK): %v", err)
	}
	if string(got) != `"click"` {
		t.Fatalf("json.Marshal(ActionKinds.CLICK) = %s, want %q", got, `"click"`)
	}

	parsed, err := computer.ParseActionKind("click")
	if err != nil {
		t.Fatalf("ParseActionKind(click): %v", err)
	}
	if parsed != computer.ActionKinds.CLICK {
		t.Fatalf("ParseActionKind(click) = %v, want %v", parsed, computer.ActionKinds.CLICK)
	}

	var unmarshaled computer.ActionKind
	if err := json.Unmarshal([]byte(`"click"`), &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal(click): %v", err)
	}
	if unmarshaled != computer.ActionKinds.CLICK {
		t.Fatalf("json.Unmarshal(click) = %v, want %v", unmarshaled, computer.ActionKinds.CLICK)
	}

	if _, err := computer.ParseActionKind("nope"); err == nil {
		t.Fatal("ParseActionKind(nope) succeeded, want error")
	}
}

func TestTriggerKindJSONAndParse(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(computer.TriggerKinds.NAVIGATIONCOMPLETE)
	if err != nil {
		t.Fatalf("json.Marshal(TriggerKinds.NAVIGATIONCOMPLETE): %v", err)
	}
	if string(got) != `"navigation_complete"` {
		t.Fatalf("json.Marshal(TriggerKinds.NAVIGATIONCOMPLETE) = %s, want %q", got, `"navigation_complete"`)
	}

	parsed, err := computer.ParseTriggerKind("navigation_complete")
	if err != nil {
		t.Fatalf("ParseTriggerKind(navigation_complete): %v", err)
	}
	if parsed != computer.TriggerKinds.NAVIGATIONCOMPLETE {
		t.Fatalf("ParseTriggerKind(navigation_complete) = %v, want %v", parsed, computer.TriggerKinds.NAVIGATIONCOMPLETE)
	}

	var unmarshaled computer.TriggerKind
	if err := json.Unmarshal([]byte(`"navigation_complete"`), &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal(navigation_complete): %v", err)
	}
	if unmarshaled != computer.TriggerKinds.NAVIGATIONCOMPLETE {
		t.Fatalf("json.Unmarshal(navigation_complete) = %v, want %v", unmarshaled, computer.TriggerKinds.NAVIGATIONCOMPLETE)
	}

	if _, err := computer.ParseTriggerKind("nope"); err == nil {
		t.Fatal("ParseTriggerKind(nope) succeeded, want error")
	}
}

func TestSurfaceKindJSONAndParse(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(computer.SurfaceKinds.BROWSER)
	if err != nil {
		t.Fatalf("json.Marshal(SurfaceKinds.BROWSER): %v", err)
	}
	if string(got) != `"browser"` {
		t.Fatalf("json.Marshal(SurfaceKinds.BROWSER) = %s, want %q", got, `"browser"`)
	}

	parsed, err := computer.ParseSurfaceKind("browser")
	if err != nil {
		t.Fatalf("ParseSurfaceKind(browser): %v", err)
	}
	if parsed != computer.SurfaceKinds.BROWSER {
		t.Fatalf("ParseSurfaceKind(browser) = %v, want %v", parsed, computer.SurfaceKinds.BROWSER)
	}

	var unmarshaled computer.SurfaceKind
	if err := json.Unmarshal([]byte(`"browser"`), &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal(browser): %v", err)
	}
	if unmarshaled != computer.SurfaceKinds.BROWSER {
		t.Fatalf("json.Unmarshal(browser) = %v, want %v", unmarshaled, computer.SurfaceKinds.BROWSER)
	}

	if _, err := computer.ParseSurfaceKind("nope"); err == nil {
		t.Fatal("ParseSurfaceKind(nope) succeeded, want error")
	}
}
