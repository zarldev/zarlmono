package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	protocolVersion20260728 = "2026-07-28"
	protocolVersion20251125 = "2025-11-25"
	protocolVersion20241105 = "2024-11-05"
	protocolMetaKey         = "io.modelcontextprotocol/protocolVersion"
)

// ensureReady owns negotiation separately from public tools/list discovery.
// HTTP construction stays lazy and custom transports keep their explicit policy.
func (c *Client) ensureReady(ctx context.Context) error {
	if !c.negotiate {
		return nil
	}
	select {
	case c.readyGate <- struct{}{}:
		defer func() { <-c.readyGate }()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	c.protocolM.Lock()
	ready := c.ready
	c.protocolM.Unlock()
	if ready {
		return nil
	}
	if transport, ok := c.transport.(*httpTransport); ok {
		transport.setProtocol(protocolVersion20260728)
	}
	probeParams, err := paramsFor20260728(nil)
	if err != nil {
		return err
	}
	probeContext := ctx
	stopProbe := func() {}
	_, stdio := c.transport.(*stdioTransport)
	if stdio {
		probeContext, stopProbe = context.WithTimeout(ctx, 750*time.Millisecond)
	}
	raw, err := c.rawCall(probeContext, "server/discover", probeParams, false)
	stopProbe()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err == nil {
		var discovered struct {
			ResultType   string          `json:"resultType"`
			Supported    []string        `json:"supportedVersions"`
			Capabilities json.RawMessage `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &discovered); err != nil {
			return fmt.Errorf("parse server discovery: %w", err)
		}
		if discovered.ResultType == resultTypeComplete {
			var capabilities map[string]json.RawMessage
			if json.Unmarshal(discovered.Capabilities, &capabilities) != nil || capabilities == nil {
				return errors.New("MCP server discovery capabilities must be an object")
			}
			for _, version := range discovered.Supported {
				if version == protocolVersion20260728 {
					c.setReady(protocolVersion20260728)
					return nil
				}
			}
			return errors.New("MCP server does not support protocol revision 2026-07-28")
		}
		return errors.New("MCP server discovery returned an unsupported result")
	}
	var rpc *rpcError
	var status *httpStatusError
	needsInitialize := errors.As(err, &rpc) && rpc.Code == -32601
	if stdio && err != nil {
		needsInitialize = !errors.As(err, &rpc) || (rpc.Code != -32020 && rpc.Code != -32021 && rpc.Code != -32022)
	}
	if errors.As(err, &status) {
		needsInitialize = status.Status == http.StatusBadRequest || (status.Status == http.StatusOK && needsInitialize)
		if status.RPC != nil && (status.RPC.Code == -32020 || status.RPC.Code == -32021 || status.RPC.Code == -32022) {
			needsInitialize = false // Explicit protocol errors must not downgrade.
		}
	}
	if !needsInitialize {
		return fmt.Errorf("MCP discovery negotiation: %w", err)
	}
	if transport, ok := c.transport.(*httpTransport); ok {
		transport.setProtocol("")
	}
	return c.initialize(ctx)
}

func (c *Client) setReady(version string) {
	c.protocolM.Lock()
	c.protocol, c.ready = version, true
	c.protocolM.Unlock()
	if transport, ok := c.transport.(*stdioTransport); ok {
		transport.setProtocol(version)
	}
	if transport, ok := c.transport.(*httpTransport); ok {
		transport.setProtocol(version)
	}
}

func validateInitializeResult(raw json.RawMessage) (initializeResult, error) {
	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return initializeResult{}, fmt.Errorf("parse initialize result: %w", err)
	}
	if result.ProtocolVersion != protocolVersion20251125 && result.ProtocolVersion != protocolVersion20241105 {
		return initializeResult{}, fmt.Errorf("unsupported negotiated MCP revision %q", result.ProtocolVersion)
	}
	if result.Capabilities == nil {
		return initializeResult{}, errors.New("MCP initialize capabilities must be an object")
	}
	return result, nil
}

func paramsFor20260728(params any) (map[string]json.RawMessage, error) {
	object := make(map[string]json.RawMessage)
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode MCP parameters: %w", err)
		}
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, fmt.Errorf("decode MCP parameter object: %w", err)
		}
		if object == nil {
			return nil, errors.New("MCP parameters must be an object")
		}
	}
	metadata, err := json.Marshal(map[string]any{protocolMetaKey: protocolVersion20260728,
		"io.modelcontextprotocol/clientInfo": ClientInfo, "io.modelcontextprotocol/clientCapabilities": map[string]any{}})
	if err != nil {
		return nil, fmt.Errorf("encode MCP client identity: %w", err)
	}
	if previous, present := object["_meta"]; present {
		var carried map[string]json.RawMessage
		if json.Unmarshal(previous, &carried) != nil || carried == nil {
			return nil, errors.New("MCP metadata must be an object")
		}
		var identity map[string]json.RawMessage
		if err := json.Unmarshal(metadata, &identity); err != nil {
			return nil, fmt.Errorf("decode MCP identity: %w", err)
		}
		for key, value := range identity {
			carried[key] = value
		}
		metadata, err = json.Marshal(carried)
		if err != nil {
			return nil, fmt.Errorf("merge MCP metadata: %w", err)
		}
	}
	object["_meta"] = metadata
	return object, nil
}

func requestProtocol(params any) string {
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	var object struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(data, &object) != nil {
		return ""
	}
	var version string
	_ = json.Unmarshal(object.Meta[protocolMetaKey], &version)
	return version
}

func encodeProtocolHeader(value string) string {
	if value == strings.TrimSpace(value) && !strings.HasPrefix(value, "=?base64?") {
		safe := true
		for _, char := range value {
			if char < 32 || char > 126 {
				safe = false
				break
			}
		}
		if safe {
			return value
		}
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

func decodeProtocolHeader(value string) (string, error) {
	if strings.HasPrefix(value, "=?base64?") {
		if !strings.HasSuffix(value, "?=") {
			return "", errors.New("invalid encoded MCP header")
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?="))
		return string(data), err
	}
	return value, nil
}

func validateCompleteResult(raw json.RawMessage) error {
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.ResultType != resultTypeComplete {
		return fmt.Errorf("unsupported MCP result discriminator %q", result.ResultType)
	}
	return nil
}

type httpStatusError struct {
	Status int
	RPC    *rpcError
}

func (e *httpStatusError) Error() string {
	if e.RPC != nil {
		return fmt.Sprintf("MCP HTTP status %d: %s", e.Status, e.RPC.Error())
	}
	return fmt.Sprintf("MCP HTTP status %d", e.Status)
}
func (e *httpStatusError) Unwrap() error {
	if e.RPC == nil {
		return nil
	}
	return e.RPC
}
