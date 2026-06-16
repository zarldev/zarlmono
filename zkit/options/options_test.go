package options_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
)

// Example struct to test the options pattern.
type TestConfig struct {
	Name    string
	Timeout time.Duration
	Count   int
}

func NewTestConfig(opts ...options.Option[TestConfig]) *TestConfig {
	cfg := &TestConfig{
		Name:    "default",
		Timeout: 30 * time.Second,
		Count:   1,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}

func WithName(name string) options.Option[TestConfig] {
	return func(cfg *TestConfig) {
		cfg.Name = name
	}
}

func WithTimeout(timeout time.Duration) options.Option[TestConfig] {
	return func(cfg *TestConfig) {
		cfg.Timeout = timeout
	}
}

func WithCount(count int) options.Option[TestConfig] {
	return func(cfg *TestConfig) {
		cfg.Count = count
	}
}

func TestOptionsPattern(t *testing.T) {
	tests := []struct {
		name     string
		opts     []options.Option[TestConfig]
		expected TestConfig
	}{
		{
			name: "defaults only",
			opts: nil,
			expected: TestConfig{
				Name:    "default",
				Timeout: 30 * time.Second,
				Count:   1,
			},
		},
		{
			name: "single option",
			opts: []options.Option[TestConfig]{
				WithName("test"),
			},
			expected: TestConfig{
				Name:    "test",
				Timeout: 30 * time.Second,
				Count:   1,
			},
		},
		{
			name: "multiple options",
			opts: []options.Option[TestConfig]{
				WithName("production"),
				WithTimeout(60 * time.Second),
				WithCount(5),
			},
			expected: TestConfig{
				Name:    "production",
				Timeout: 60 * time.Second,
				Count:   5,
			},
		},
		{
			name: "override same option",
			opts: []options.Option[TestConfig]{
				WithName("first"),
				WithName("second"), // should override
			},
			expected: TestConfig{
				Name:    "second",
				Timeout: 30 * time.Second,
				Count:   1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewTestConfig(tt.opts...)

			if cfg.Name != tt.expected.Name {
				t.Errorf("Name = %v, want %v", cfg.Name, tt.expected.Name)
			}
			if cfg.Timeout != tt.expected.Timeout {
				t.Errorf("Timeout = %v, want %v", cfg.Timeout, tt.expected.Timeout)
			}
			if cfg.Count != tt.expected.Count {
				t.Errorf("Count = %v, want %v", cfg.Count, tt.expected.Count)
			}
		})
	}
}

func TestOptionsChaining(t *testing.T) {
	cfg := NewTestConfig(
		WithName("chained"),
		WithTimeout(2*time.Minute),
		WithCount(10),
	)

	expected := TestConfig{
		Name:    "chained",
		Timeout: 2 * time.Minute,
		Count:   10,
	}

	if *cfg != expected {
		t.Errorf("got %+v, want %+v", *cfg, expected)
	}
}
