package computer

// ObservationImage contains a rendered image attached to an observation.
type ObservationImage struct {
	MIMEType string `json:"mime_type,omitempty"`
	DataURI  string `json:"data_uri,omitempty"`
}
