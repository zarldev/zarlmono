package mcp

import (
	"encoding/json"
	"net/http"
)

func (s *Server) modernRequestError(request *http.Request, rpc rpcRequest) *rpcError {
	headerVersion := request.Header.Get(headerProtocolVersion)
	if headerVersion != "" && headerVersion != protocolVersion20260728 && headerVersion != protocolVersion20251125 {
		return unsupportedProtocol(headerVersion)
	}
	data, err := json.Marshal(rpc.Params)
	if err != nil {
		return &rpcError{Code: -32602, Message: "invalid parameters"}
	}
	var parameters map[string]json.RawMessage
	if json.Unmarshal(data, &parameters) != nil {
		return &rpcError{Code: -32602, Message: "parameters must be an object"}
	}
	metadata, present := parameters["_meta"]
	if !present {
		if headerVersion == protocolVersion20260728 {
			return &rpcError{Code: -32020, Message: "missing 2026-07-28 metadata"}
		}
		return nil
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(metadata, &meta) != nil || meta == nil {
		return &rpcError{Code: -32602, Message: "metadata must be an object"}
	}
	var version string
	if json.Unmarshal(meta[protocolMetaKey], &version) != nil {
		return &rpcError{Code: -32602, Message: "invalid metadata protocol version"}
	}
	if version != protocolVersion20260728 {
		return unsupportedProtocol(version)
	}
	method, err := decodeProtocolHeader(request.Header.Get("Mcp-Method"))
	if err != nil || method != rpc.Method || headerVersion != version {
		return &rpcError{Code: -32020, Message: "MCP mirrored header mismatch"}
	}
	capabilities, present := meta["io.modelcontextprotocol/clientCapabilities"]
	if !present {
		return &rpcError{Code: -32021, Message: "missing client capabilities"}
	}
	var capabilityObject map[string]json.RawMessage
	if json.Unmarshal(capabilities, &capabilityObject) != nil || capabilityObject == nil {
		return &rpcError{Code: -32602, Message: "client capabilities must be an object"}
	}
	if rpc.Method == methodToolsCall || rpc.Method == methodPromptsGet || rpc.Method == methodResourcesRead {
		field := "name"
		if rpc.Method == methodResourcesRead {
			field = "uri"
		}
		var expected string
		if json.Unmarshal(parameters[field], &expected) != nil {
			return &rpcError{Code: -32602, Message: "invalid named parameter"}
		}
		name, err := decodeProtocolHeader(request.Header.Get("Mcp-Name"))
		if err != nil || name != expected {
			return &rpcError{Code: -32020, Message: "MCP name header mismatch"}
		}
		if rpc.Method == methodToolsCall {
			s.mu.RLock()
			tool, known := s.tools[expected]
			s.mu.RUnlock()
			if known {
				bindings, bindingErr := schemaHeaderBindings(tool.def.InputSchema)
				if bindingErr != nil {
					return &rpcError{Code: -32602, Message: "invalid tool header bindings"}
				}
				arguments := parameters["arguments"]
				if len(arguments) == 0 {
					arguments = json.RawMessage(`{}`)
				}
				if bindingErr := verifySchemaHeaders(request.Header, arguments, bindings); bindingErr != nil {
					return &rpcError{Code: -32020, Message: "MCP parameter header mismatch"}
				}
			}
		}
	}
	return nil
}

func unsupportedProtocol(version string) *rpcError {
	data, _ := json.Marshal(map[string]any{"supported": []string{protocolVersion20260728, protocolVersion20251125}, "requested": version})
	return &rpcError{Code: -32022, Message: "Unsupported protocol version", Data: data}
}

func (s *Server) handleServerDiscovery(request rpcRequest) rpcResponse {
	data, _ := json.Marshal(map[string]any{
		"resultType": resultTypeComplete, "supportedVersions": []string{protocolVersion20260728, protocolVersion20251125},
		"capabilities": map[string]any{"tools": map[string]any{}},
		"_meta":        map[string]any{"io.modelcontextprotocol/serverInfo": s.info},
	})
	return rpcResponse{JSONRPC: jsonrpcVersion, ID: request.ID, Result: data}
}

func completeResponse(response rpcResponse) rpcResponse {
	if response.Error != nil {
		return response
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(response.Result, &result) != nil {
		return response
	}
	result["resultType"] = json.RawMessage(`"` + resultTypeComplete + `"`)
	response.Result, _ = json.Marshal(result)
	return response
}
