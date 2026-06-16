package askpass

// EnvSock is the child-process environment variable carrying the Unix socket
// path used by `zarlcode --askpass` to talk back to the live TUI.
const EnvSock = "ZARLCODE_ASKPASS_SOCK"

// Request is the JSON line sent from the askpass helper to the TUI.
type Request struct {
	Prompt string `json:"prompt"`
}

// Response is the JSON line sent from the TUI to the askpass helper.
type Response struct {
	Password string `json:"password,omitempty"`
	Error    string `json:"error,omitempty"`
}
