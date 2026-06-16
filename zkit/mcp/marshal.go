package mcp

import "encoding/json"

// MarshalJSON emits a TextContent with the wire-format `type`
// discriminator the MCP spec requires. The bare struct's JSON tag set
// only covers `text`; the discriminator is added here so server-side
// CallResult emission stays correct without forcing every consumer
// to wrap their content in custom types.
func (t TextContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{contentTypeText, t.Text})
}

// MarshalJSON emits an ImageContent with a `type` discriminator.
func (i ImageContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	}{contentTypeImage, i.Data, i.MIMEType})
}

// MarshalJSON emits an AudioContent with a `type` discriminator.
func (a AudioContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	}{contentTypeAudio, a.Data, a.MIMEType})
}

// MarshalJSON emits a ResourceContent with a `type` discriminator.
// Empty Text/Blob are omitted so a URI-only reference round-trips
// cleanly.
func (r ResourceContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     string `json:"type"`
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType,omitempty"`
		Text     string `json:"text,omitempty"`
		Blob     string `json:"blob,omitempty"`
	}{contentTypeResource, r.URI, r.MIMEType, r.Text, r.Blob})
}

// marshalCallResult emits a CallResult with the typed Content slice
// in the MCP wire shape: {"content":[...], "isError":bool}. Each
// Content variant's MarshalJSON adds its own `type` discriminator.
func marshalCallResult(r CallResult) ([]byte, error) {
	out := struct {
		Content []Content `json:"content"`
		IsError bool      `json:"isError,omitempty"`
	}{
		Content: r.Content,
		IsError: r.IsError,
	}
	if out.Content == nil {
		out.Content = []Content{}
	}
	return json.Marshal(out)
}
