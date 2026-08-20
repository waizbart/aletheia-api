package config

import (
	"log"
	"os"
	"strconv"
	"time"
)

var Fatalf = log.Fatalf

func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		Fatalf("required environment variable %s is not set", key)
	}
	return v
}

func EnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// EnvIntOrDefault reads an integer setting. A malformed value is a
// configuration error rather than something to silently paper over with the
// fallback: an operator who writes RATE_LIMIT_RPS=ten wants to hear about it.
func EnvIntOrDefault(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		Fatalf("environment variable %s must be an integer, got %q", key, raw)
		return fallback
	}
	return v
}

// EnvDurationOrDefault reads a Go duration string (e.g. "5s", "2m").
func EnvDurationOrDefault(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		Fatalf("environment variable %s must be a duration (e.g. 30s), got %q", key, raw)
		return fallback
	}
	return v
}

// EnvBoolOrDefault reads a boolean setting accepting the strconv.ParseBool set
// ("1", "t", "true", "0", "f", "false", …).
func EnvBoolOrDefault(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		Fatalf("environment variable %s must be a boolean, got %q", key, raw)
		return fallback
	}
	return v
}
