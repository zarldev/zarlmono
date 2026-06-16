package dynamic

//go:generate go tool goenums -f transport_enum.go

// transport is the goenums source for Transport — MCP connection transport type.
// The trailing comment on each constant is the stable wire/config identifier
// (what appears in the transport field of mcp_connect arguments).
type transport int

const (
	transportStdio transport = iota // stdio
	transportHTTP                   // http
)
