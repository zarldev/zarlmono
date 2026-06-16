package homeassistant_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
)

func TestClientGetState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states/sensor.temperature" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}

		json.NewEncoder(w).Encode(homeassistant.EntityState{
			EntityID:   "sensor.temperature",
			State:      "22.5",
			Attributes: map[string]any{"unit_of_measurement": "°C", "friendly_name": "Living Room Temperature"},
		})
	}))
	defer srv.Close()

	c := homeassistant.NewClient(srv.URL, "test-token")
	state, err := c.GetState(t.Context(), "sensor.temperature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.State != "22.5" {
		t.Errorf("state = %q, want 22.5", state.State)
	}
	if state.EntityID != "sensor.temperature" {
		t.Errorf("entity_id = %q", state.EntityID)
	}
}

func TestClientCallService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/light/turn_on" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["entity_id"] != "light.living_room" {
			t.Errorf("entity_id = %v", body["entity_id"])
		}

		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	c := homeassistant.NewClient(srv.URL, "test-token")
	err := c.CallService(t.Context(), "light", "turn_on", "light.living_room", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientListStates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			t.Errorf("path = %s", r.URL.Path)
		}

		json.NewEncoder(w).Encode([]homeassistant.EntityState{
			{EntityID: "light.living_room", State: "on", Attributes: map[string]any{"friendly_name": "Living Room"}},
			{EntityID: "sensor.temperature", State: "22", Attributes: map[string]any{"friendly_name": "Temperature"}},
			{EntityID: "light.bedroom", State: "off", Attributes: map[string]any{"friendly_name": "Bedroom"}},
		})
	}))
	defer srv.Close()

	c := homeassistant.NewClient(srv.URL, "test-token")
	states, err := c.ListStates(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("len = %d, want 3", len(states))
	}
}
