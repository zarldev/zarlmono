package homeassistant_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestGetStateTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(homeassistant.EntityState{
			EntityID:   "sensor.temperature",
			State:      "22.5",
			Attributes: map[string]any{"unit_of_measurement": "°C", "friendly_name": "Living Room Temperature"},
		})
	}))
	defer srv.Close()

	tool := homeassistant.NewGetStateTool(homeassistant.NewClient(srv.URL, "token"))

	t.Run("definition", func(t *testing.T) {
		def := tool.Definition()
		if def.Name.String() != "get_state" {
			t.Errorf("name = %s", def.Name)
		}
	})

	t.Run("execute", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"entity_id": "sensor.temperature"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !result.Success {
			t.Fatalf("unexpected tool failure: %s", result.Error)
		}
		content := service.ToolResultText(result)
		if !strings.Contains(content, "22.5") {
			t.Errorf("content = %q, should contain 22.5", content)
		}
		if !strings.Contains(content, "Living Room Temperature") {
			t.Errorf("content = %q, should contain friendly name", content)
		}
	})

	t.Run("missing entity_id", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if result.Success {
			t.Fatalf("expected failure, got %q", service.ToolResultText(result))
		}
	})
}

func TestCallServiceTool(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	tool := homeassistant.NewCallServiceTool(homeassistant.NewClient(srv.URL, "token"))

	t.Run("definition", func(t *testing.T) {
		def := tool.Definition()
		if def.Name.String() != "call_service" {
			t.Errorf("name = %s", def.Name)
		}
	})

	t.Run("execute", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
			"domain":    "light",
			"service":   "turn_on",
			"entity_id": "light.living_room",
			"data":      map[string]any{"brightness": float64(128)},
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !result.Success {
			t.Fatalf("unexpected tool failure: %s", result.Error)
		}
		if !strings.Contains(service.ToolResultText(result), "light.turn_on") {
			t.Errorf("content = %q", service.ToolResultText(result))
		}
	})

	t.Run("missing required args", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"domain": "light"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if result.Success {
			t.Fatalf("expected failure, got %q", service.ToolResultText(result))
		}
	})
}

func TestListEntitiesTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]homeassistant.EntityState{
			{EntityID: "light.living_room", State: "on", Attributes: map[string]any{"friendly_name": "Living Room"}},
			{EntityID: "sensor.temperature", State: "22", Attributes: map[string]any{"friendly_name": "Temperature"}},
			{EntityID: "light.bedroom", State: "off", Attributes: map[string]any{"friendly_name": "Bedroom"}},
		})
	}))
	defer srv.Close()

	tool := homeassistant.NewListEntitiesTool(homeassistant.NewClient(srv.URL, "token"))

	t.Run("all entities", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !result.Success {
			t.Fatalf("unexpected tool failure: %s", result.Error)
		}
		content := service.ToolResultText(result)
		if !strings.Contains(content, "light.living_room") {
			t.Errorf("missing light.living_room in %q", content)
		}
		if !strings.Contains(content, "sensor.temperature") {
			t.Errorf("missing sensor.temperature in %q", content)
		}
	})

	t.Run("filtered by domain", func(t *testing.T) {
		result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"domain": "light"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !result.Success {
			t.Fatalf("unexpected tool failure: %s", result.Error)
		}
		content := service.ToolResultText(result)
		if !strings.Contains(content, "light.living_room") {
			t.Errorf("missing light.living_room")
		}
		if strings.Contains(content, "sensor.temperature") {
			t.Errorf("should not contain sensor entities when filtering by light")
		}
	})
}
