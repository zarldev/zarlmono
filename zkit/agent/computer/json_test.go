package computer_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

func TestActionRequestJSONShape(t *testing.T) {
	t.Parallel()

	req := computer.ActionRequest{
		Action: computer.Action{
			Kind: computer.ActionKinds.CLICK,
			Target: &computer.TargetRef{
				ID:      "target-1",
				Role:    "button",
				Name:    "Submit",
				Text:    "Submit",
				Locator: "aria/Submit",
				Position: &computer.Point{
					X: 10,
					Y: 20,
				},
			},
			Value: "ignored",
			Key:   "Enter",
			URL:   "https://example.com/next",
			Delta: &computer.Point{X: 1, Y: 2},
		},
		When: &computer.Trigger{
			Kind: computer.TriggerKinds.VISIBLE,
			Target: &computer.TargetRef{
				ID: "target-1",
			},
		},
		Until: &computer.Trigger{
			Kind: computer.TriggerKinds.NAVIGATIONCOMPLETE,
			URL:  "https://example.com/done",
		},
	}

	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(ActionRequest): %v", err)
	}

	want := `{"action":{"kind":"click","target":{"id":"target-1","role":"button","name":"Submit","text":"Submit","locator":"aria/Submit","position":{"x":10,"y":20}},"value":"ignored","key":"Enter","url":"https://example.com/next","delta":{"x":1,"y":2}},"when":{"kind":"visible","target":{"id":"target-1"}},"until":{"kind":"navigation_complete","url":"https://example.com/done"}}`
	if string(got) != want {
		t.Fatalf("json.Marshal(ActionRequest) = %s\nwant %s", got, want)
	}

	var roundTrip computer.ActionRequest
	if err := json.Unmarshal(got, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal(ActionRequest): %v", err)
	}
	if !reflect.DeepEqual(roundTrip, req) {
		t.Fatalf("ActionRequest round-trip mismatch\ngot:  %#v\nwant: %#v", roundTrip, req)
	}
}

func TestObservationJSONShape(t *testing.T) {
	t.Parallel()

	obs := computer.Observation{
		Surface: computer.SurfaceInfo{
			Kind:   computer.SurfaceKinds.BROWSER,
			Title:  "Example",
			URL:    "https://example.com",
			Width:  1280,
			Height: 720,
		},
		FocusedTarget: &computer.TargetDescriptor{
			ID:      "input-1",
			Role:    "textbox",
			Name:    "Search",
			Visible: true,
			Focused: true,
			Bounds: &computer.Rect{
				X:      10,
				Y:      20,
				Width:  300,
				Height: 40,
			},
		},
		Targets: []computer.TargetDescriptor{
			{
				ID:          "button-1",
				Role:        "button",
				Name:        "Submit",
				Text:        "Submit",
				Description: "Primary form action",
				Bounds: &computer.Rect{
					X:      100,
					Y:      200,
					Width:  90,
					Height: 32,
				},
				Enabled: true,
				Visible: true,
				Value:   "go",
			},
		},
		VisibleText: "Example Domain",
		Screenshot: &computer.ObservationImage{
			MIMEType: "image/png",
			DataURI:  "data:image/png;base64,AAAA",
		},
		Hints: []string{"ready"},
	}

	got, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("json.Marshal(Observation): %v", err)
	}

	want := `{"surface":{"kind":"browser","title":"Example","url":"https://example.com","width":1280,"height":720},"focused_target":{"id":"input-1","role":"textbox","name":"Search","bounds":{"x":10,"y":20,"width":300,"height":40},"visible":true,"focused":true},"targets":[{"id":"button-1","role":"button","name":"Submit","text":"Submit","description":"Primary form action","bounds":{"x":100,"y":200,"width":90,"height":32},"enabled":true,"visible":true,"value":"go"}],"visible_text":"Example Domain","screenshot":{"mime_type":"image/png","data_uri":"data:image/png;base64,AAAA"},"hints":["ready"]}`
	if string(got) != want {
		t.Fatalf("json.Marshal(Observation) = %s\nwant %s", got, want)
	}

	var roundTrip computer.Observation
	if err := json.Unmarshal(got, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal(Observation): %v", err)
	}
	if !reflect.DeepEqual(roundTrip, obs) {
		t.Fatalf("Observation round-trip mismatch\ngot:  %#v\nwant: %#v", roundTrip, obs)
	}
}

func TestOptionalFieldsOmitCleanly(t *testing.T) {
	t.Parallel()

	req := computer.ActionRequest{
		Action: computer.Action{Kind: computer.ActionKinds.SCROLL},
	}

	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(minimal ActionRequest): %v", err)
	}
	want := `{"action":{"kind":"scroll"}}`
	if string(got) != want {
		t.Fatalf("json.Marshal(minimal ActionRequest) = %s, want %s", got, want)
	}

	obs := computer.Observation{
		Surface: computer.SurfaceInfo{Kind: computer.SurfaceKinds.BROWSER},
	}

	got, err = json.Marshal(obs)
	if err != nil {
		t.Fatalf("json.Marshal(minimal Observation): %v", err)
	}
	want = `{"surface":{"kind":"browser"}}`
	if string(got) != want {
		t.Fatalf("json.Marshal(minimal Observation) = %s, want %s", got, want)
	}
}
