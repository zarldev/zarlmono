package mcp

const (
	jsonrpcVersion = "2.0"

	// Match the stdio receive bound, with room for large tool descriptions
	// and their JSON-RPC envelope. Requests and notifications retain 4 MiB caps.
	maxRPCResponseBytes = 16 * 1024 * 1024

	contentTypeText     = "text"
	contentTypeImage    = "image"
	contentTypeAudio    = "audio"
	contentTypeResource = "resource"

	methodInitialize               = "initialize"
	methodToolsCall                = "tools/call"
	methodPromptsGet               = "prompts/get"
	methodResourcesRead            = "resources/read"
	methodSubscriptionsListen      = "subscriptions/listen"
	methodSubscriptionAcknowledged = "notifications/subscriptions/acknowledged"
	resultTypeComplete             = "complete"

	headerProtocolVersion = "Mcp-Protocol-Version"
	headerSessionID       = "Mcp-Session-Id"
	headerParameterPrefix = "Mcp-Param-"
)
