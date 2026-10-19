package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (t *httpTransport) setProtocol(version string) {
	t.protocolMu.Lock()
	t.version = version
	t.protocolMu.Unlock()
}

func (t *httpTransport) clearExpiredSession(expired string) bool {
	t.protocolMu.Lock()
	defer t.protocolMu.Unlock()
	if expired == "" || t.session != expired {
		return false
	}
	t.version = ""
	t.session = ""
	t.headerBindings = nil
	t.invalidHeaderBindings = nil
	t.headerBindingsReady = false
	return true
}

func (t *httpTransport) closeSession() {
	t.protocolMu.Lock()
	version, session := t.version, t.session
	t.protocolMu.Unlock()
	if session == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.baseURL, nil)
	if err != nil {
		return
	}
	if version != "" {
		request.Header.Set(headerProtocolVersion, version)
	}
	request.Header.Set(headerSessionID, session)
	if t.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+t.authToken)
	}
	response, err := t.http.Do(request)
	if err != nil {
		return
	}
	_ = response.Body.Close()
}

func (t *httpTransport) protocolHeaders(request *http.Request, rpc rpcRequest) error {
	t.protocolMu.Lock()
	version, session := t.version, t.session
	bindings := make(map[string][]schemaHeaderBinding, len(t.headerBindings))
	for name, toolBindings := range t.headerBindings {
		bindings[name] = append([]schemaHeaderBinding(nil), toolBindings...)
	}
	invalidBindings := make(map[string]bool, len(t.invalidHeaderBindings))
	for name, invalid := range t.invalidHeaderBindings {
		invalidBindings[name] = invalid
	}
	t.protocolMu.Unlock()
	if version != "" {
		request.Header.Set(headerProtocolVersion, version)
	}
	if session != "" {
		request.Header.Set(headerSessionID, session)
	}
	if version != protocolVersion20260728 || rpc.Method == "" {
		return nil
	}
	request.Header.Set("Mcp-Method", encodeProtocolHeader(rpc.Method))
	if rpc.Method != methodToolsCall && rpc.Method != methodPromptsGet && rpc.Method != methodResourcesRead {
		return nil
	}
	data, err := json.Marshal(rpc.Params)
	if err != nil {
		return fmt.Errorf("encode MCP named parameters: %w", err)
	}
	var parameters struct {
		Name      string          `json:"name"`
		URI       string          `json:"uri"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &parameters); err != nil {
		return fmt.Errorf("decode MCP named parameters: %w", err)
	}
	name := parameters.Name
	if rpc.Method == methodResourcesRead {
		name = parameters.URI
	}
	request.Header.Set("Mcp-Name", encodeProtocolHeader(name))
	if rpc.Method == methodToolsCall {
		if invalidBindings[name] {
			return fmt.Errorf("MCP tool %q has an invalid discovered header definition", name)
		}
		toolBindings := bindings[name]
		if err := applySchemaHeaders(request.Header, parameters.Arguments, toolBindings); err != nil {
			return fmt.Errorf("encode MCP parameter headers: %w", err)
		}
	}
	return nil
}

func (t *httpTransport) schemaHeadersReady() bool {
	t.protocolMu.Lock()
	defer t.protocolMu.Unlock()
	return t.headerBindingsReady
}

func (t *httpTransport) setSchemaHeaders(bindings map[string][]schemaHeaderBinding, invalid map[string]bool) {
	t.protocolMu.Lock()
	t.headerBindings = bindings
	t.invalidHeaderBindings = invalid
	t.headerBindingsReady = true
	t.protocolMu.Unlock()
}

// Notify acknowledges only an accepted, empty notification response. It never
// interprets a tool response or redirects as an acknowledgement.
func (t *httpTransport) Notify(ctx context.Context, notification rpcNotification) error {
	_, err := t.Call(ctx, rpcRequest{JSONRPC: notification.JSONRPC, Method: notification.Method, Params: notification.Params})
	return err
}

func (t *httpTransport) acceptSession(response *http.Response) error {
	session := response.Header.Get(headerSessionID)
	if session == "" {
		return nil
	}
	for _, character := range session {
		if character < 33 || character > 126 {
			return errors.New("invalid MCP session identifier")
		}
	}
	t.protocolMu.Lock()
	t.session = session
	t.protocolMu.Unlock()
	return nil
}

func notificationResponse(response *http.Response) error {
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("MCP notification acknowledgement status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1))
	if err != nil {
		return fmt.Errorf("read notification acknowledgement: %w", err)
	}
	if len(body) != 0 {
		return errors.New("MCP notification acknowledgement contains a body")
	}
	return nil
}
