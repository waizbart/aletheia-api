package config

import (
	"log"
	"os"
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
