package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/config"
)

func TestMustEnv_Present(t *testing.T) {
	t.Setenv("TEST_MUST_ENV", "value")

	got := config.MustEnv("TEST_MUST_ENV")
	if got != "value" {
		t.Errorf("got %q, want %q", got, "value")
	}
}

func TestMustEnv_Missing(t *testing.T) {
	os.Unsetenv("TEST_MUST_ENV_MISSING")

	original := config.Fatalf
	defer func() { config.Fatalf = original }()

	var called bool
	config.Fatalf = func(format string, args ...any) {
		called = true
	}

	config.MustEnv("TEST_MUST_ENV_MISSING")
	if !called {
		t.Fatal("expected Fatalf to be called")
	}
}

func TestEnvOrDefault_Present(t *testing.T) {
	t.Setenv("TEST_ENV_DEFAULT", "custom")

	got := config.EnvOrDefault("TEST_ENV_DEFAULT", "fallback")
	if got != "custom" {
		t.Errorf("got %q, want %q", got, "custom")
	}
}

func TestEnvOrDefault_Missing(t *testing.T) {
	os.Unsetenv("TEST_ENV_DEFAULT_MISSING")

	got := config.EnvOrDefault("TEST_ENV_DEFAULT_MISSING", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

// captureFatalf swaps config.Fatalf for the duration of a test and reports
// whether it fired.
func captureFatalf(t *testing.T) *bool {
	t.Helper()
	original := config.Fatalf
	t.Cleanup(func() { config.Fatalf = original })

	var called bool
	config.Fatalf = func(format string, args ...any) { called = true }
	return &called
}

func TestEnvIntOrDefault(t *testing.T) {
	tests := []struct {
		name      string
		set       bool
		value     string
		fallback  int
		want      int
		wantFatal bool
	}{
		{name: "unset uses fallback", fallback: 20, want: 20},
		{name: "parses a value", set: true, value: "5", fallback: 20, want: 5},
		{name: "parses a negative value", set: true, value: "-1", fallback: 20, want: -1},
		{name: "malformed value is fatal", set: true, value: "ten", fallback: 20, want: 20, wantFatal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_ENV_INT"
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				os.Unsetenv(key)
			}
			fatal := captureFatalf(t)

			if got := config.EnvIntOrDefault(key, tt.fallback); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
			if *fatal != tt.wantFatal {
				t.Errorf("Fatalf called = %v, want %v", *fatal, tt.wantFatal)
			}
		})
	}
}

func TestEnvDurationOrDefault(t *testing.T) {
	tests := []struct {
		name      string
		set       bool
		value     string
		fallback  time.Duration
		want      time.Duration
		wantFatal bool
	}{
		{name: "unset uses fallback", fallback: time.Second, want: time.Second},
		{name: "parses a duration", set: true, value: "2m", fallback: time.Second, want: 2 * time.Minute},
		{name: "malformed value is fatal", set: true, value: "soon", fallback: time.Second, want: time.Second, wantFatal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_ENV_DURATION"
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				os.Unsetenv(key)
			}
			fatal := captureFatalf(t)

			if got := config.EnvDurationOrDefault(key, tt.fallback); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if *fatal != tt.wantFatal {
				t.Errorf("Fatalf called = %v, want %v", *fatal, tt.wantFatal)
			}
		})
	}
}

func TestEnvBoolOrDefault(t *testing.T) {
	tests := []struct {
		name      string
		set       bool
		value     string
		fallback  bool
		want      bool
		wantFatal bool
	}{
		{name: "unset uses fallback", fallback: true, want: true},
		{name: "parses true", set: true, value: "true", want: true},
		{name: "parses 1", set: true, value: "1", want: true},
		{name: "parses false", set: true, value: "false", fallback: true, want: false},
		{name: "malformed value is fatal", set: true, value: "yes-please", fallback: true, want: true, wantFatal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_ENV_BOOL"
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				os.Unsetenv(key)
			}
			fatal := captureFatalf(t)

			if got := config.EnvBoolOrDefault(key, tt.fallback); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if *fatal != tt.wantFatal {
				t.Errorf("Fatalf called = %v, want %v", *fatal, tt.wantFatal)
			}
		})
	}
}
