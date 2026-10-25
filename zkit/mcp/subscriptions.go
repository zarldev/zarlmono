package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
)

type notificationSelection struct {
	Tools     bool     `json:"toolsListChanged,omitempty"`
	Prompts   bool     `json:"promptsListChanged,omitempty"`
	Resources bool     `json:"resourcesListChanged,omitempty"`
	URIs      []string `json:"resourceSubscriptions,omitempty"`
}

type serverSubscription struct {
	channel    chan rpcNotification
	restricted bool
	selection  notificationSelection
}

func (subscription serverSubscription) accepts(method string, params any) bool {
	if !subscription.restricted {
		return true
	}
	switch method {
	case "notifications/tools/list_changed":
		return subscription.selection.Tools
	case "notifications/prompts/list_changed":
		return subscription.selection.Prompts
	case "notifications/resources/list_changed":
		return subscription.selection.Resources
	case "notifications/resources/updated":
		data, err := json.Marshal(params)
		if err != nil {
			return false
		}
		var resource struct {
			URI string `json:"uri"`
		}
		return json.Unmarshal(data, &resource) == nil && slices.Contains(subscription.selection.URIs, resource.URI)
	default:
		return false
	}
}

const subscriptionIDMetaKey = "io.modelcontextprotocol/subscriptionId"

func modernSubscriptionParams() (map[string]json.RawMessage, error) {
	return paramsFor20260728(map[string]any{"notifications": notificationSelection{Tools: true, Prompts: true, Resources: true}})
}

// paramsWithSubscriptionID copies params and its metadata as raw JSON members
// before adding correlation metadata. It neither rounds numbers nor mutates a
// map supplied by the notification caller.
func paramsWithSubscriptionID(params any, subscriptionID json.RawMessage) (map[string]json.RawMessage, error) {
	members := make(map[string]json.RawMessage)
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode subscription parameters: %w", err)
		}
		if err := json.Unmarshal(data, &members); err != nil || members == nil {
			return nil, errors.New("subscription parameters must be an object")
		}
	}
	metadata := make(map[string]json.RawMessage)
	if raw, ok := members["_meta"]; ok {
		if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
			return nil, errors.New("subscription metadata must be an object")
		}
	}
	metadata[subscriptionIDMetaKey] = slices.Clone(subscriptionID)
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode subscription metadata: %w", err)
	}
	members["_meta"] = rawMetadata
	return members, nil
}

func validSubscriptionNotification(params json.RawMessage, subscriptionID json.RawMessage) bool {
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil || members == nil {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(members["_meta"], &metadata) != nil || metadata == nil {
		return false
	}
	got, ok := metadata[subscriptionIDMetaKey]
	if !ok {
		return false
	}
	var compactGot, compactWant bytes.Buffer
	if json.Compact(&compactGot, got) != nil || json.Compact(&compactWant, subscriptionID) != nil {
		return false
	}
	return bytes.Equal(compactGot.Bytes(), compactWant.Bytes())
}

func (s *Server) serveSubscription(w http.ResponseWriter, request *http.Request, rpc rpcRequest) {
	data, err := json.Marshal(rpc.Params)
	if err != nil {
		writeRPCError(w, rpc.ID, -32602, "invalid subscription parameters")
		return
	}
	var parameters struct {
		Notifications *notificationSelection `json:"notifications"`
	}
	if json.Unmarshal(data, &parameters) != nil || parameters.Notifications == nil {
		writeRPCError(w, rpc.ID, -32602, "notification selection is required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	channel := make(chan rpcNotification, 16)
	s.subM.Lock()
	s.subs = append(s.subs, serverSubscription{
		channel:    channel,
		restricted: true,
		selection:  *parameters.Notifications,
	})
	s.subM.Unlock()
	defer s.unsubscribe(channel)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ackParams, err := paramsWithSubscriptionID(map[string]any{"notifications": *parameters.Notifications}, rpc.ID)
	if err != nil {
		return
	}
	ack := rpcNotification{JSONRPC: jsonrpcVersion, Method: "notifications/subscriptions/acknowledged", Params: ackParams}
	if err := writeSubscriptionEvent(w, ack); err != nil {
		return
	}
	flusher.Flush()
	for {
		select {
		case <-request.Context().Done():
			return
		case notification := <-channel:
			params, err := paramsWithSubscriptionID(notification.Params, rpc.ID)
			if err != nil {
				continue
			}
			notification.Params = params
			if err := writeSubscriptionEvent(w, notification); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSubscriptionEvent(w http.ResponseWriter, notification rpcNotification) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode subscription event: %w", err)
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func (t *httpTransport) subscriptionRequest(ctx context.Context) (*http.Request, error) {
	if t.ensureReady != nil {
		if err := t.ensureReady(ctx); err != nil {
			return nil, err
		}
	}
	t.protocolMu.Lock()
	version := t.version
	t.protocolMu.Unlock()
	if version != protocolVersion20260728 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL, nil)
		if err != nil {
			return nil, err
		}
		if err := t.protocolHeaders(request, rpcRequest{}); err != nil {
			return nil, err
		}
		return request, nil
	}
	params, err := modernSubscriptionParams()
	if err != nil {
		return nil, err
	}
	rpc := rpcRequest{JSONRPC: jsonrpcVersion, ID: json.RawMessage(`"subscription"`), Method: "subscriptions/listen", Params: params}
	data, err := json.Marshal(rpc)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if err := t.protocolHeaders(request, rpc); err != nil {
		return nil, err
	}
	return request, nil
}
