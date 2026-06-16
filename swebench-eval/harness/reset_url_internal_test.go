package harness

import "testing"

func TestRequireLoopbackURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"localhost", "http://localhost:8081/slots/0?action=erase", false},
		{"127.0.0.1", "http://127.0.0.1:8081/slots/0", false},
		{"127-loopback-range", "http://127.9.9.9:8081/", false},
		{"ipv6 loopback", "http://[::1]:8081/", false},
		{"remote host", "http://evil.example.com/erase", true},
		{"remote ip", "http://10.0.0.5:8081/", true},
		{"unparseable", "://nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireLoopbackURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("requireLoopbackURL(%q) err = %v; wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
