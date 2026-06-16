package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"time"
)

// EntityState is a Home Assistant entity's current state.
type EntityState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// Client talks to the Home Assistant REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient creates a new Home Assistant API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	reqBody := bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	return resp, nil
}

// GetState returns the current state of an entity.
func (c *Client) GetState(ctx context.Context, entityID string) (EntityState, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/states/"+entityID, nil)
	if err != nil {
		return EntityState{}, fmt.Errorf("get state %s: %w", entityID, err)
	}
	defer resp.Body.Close()

	var state EntityState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return EntityState{}, fmt.Errorf("decode state: %w", err)
	}
	return state, nil
}

// CallService invokes a Home Assistant service.
func (c *Client) CallService(ctx context.Context, domain, svc, entityID string, data map[string]any) error {
	body := map[string]any{"entity_id": entityID}
	maps.Copy(body, data)

	resp, err := c.do(ctx, http.MethodPost, "/api/services/"+domain+"/"+svc, body)
	if err != nil {
		return fmt.Errorf("call service %s.%s: %w", domain, svc, err)
	}
	resp.Body.Close()
	return nil
}

// ListStates returns all entity states.
func (c *Client) ListStates(ctx context.Context) ([]EntityState, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/states", nil)
	if err != nil {
		return nil, fmt.Errorf("list states: %w", err)
	}
	defer resp.Body.Close()

	var states []EntityState
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("decode states: %w", err)
	}
	return states, nil
}
