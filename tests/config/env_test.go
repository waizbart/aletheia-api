package config_test

import (
	"os"
	"testing"

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
