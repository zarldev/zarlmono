package llm

//go:generate go tool goenums -f model_information_enum.go

// support describes an effective feature assertion. Unknown is deliberately
// the valid zero value: missing metadata must never grant or deny support.
type support int

const (
	supportUnknown     support = iota // unknown
	supportSupported                  // supported
	supportUnsupported                // unsupported
)

// modelProtocol identifies a public inference protocol, not an adapter's
// endpoint selection or token-limit field. Adapters translate at their boundary;
// this vocabulary does not replace openai.EndpointKind or its configuration.
type modelProtocol int

const (
	protocolUnknown         modelProtocol = iota // unknown
	protocolChatCompletions                      // chat_completions
	protocolResponses                            // responses
	protocolMessages                             // messages
	protocolGenerateContent                      // generate_content
)

// metadataSource identifies how a metadata assertion was obtained. Source IDs
// and revisions remain open values on MetadataProvenance.
type metadataSource int

const (
	metadataUnknown       metadataSource = iota // unknown
	metadataCatalogue                           // catalogue
	metadataDiscovery                           // discovery
	metadataConfiguration                       // configuration
)
