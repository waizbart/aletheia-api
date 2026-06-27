package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/config"
)

// setValidEnv populates every required variable with a valid value so individual
// tests can override exactly one to assert a specific validation failure.
func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("S3_BUCKET", "bucket")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("RPC_URL", "http://localhost:8545")
	t.Setenv("FROM_ADDRESS", "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	t.Setenv("CONTRACT_ADDRESS", "0x5FbDB2315678afecb367f032d93F642f64180aa3")
	t.Setenv("API_KEYS", "secret-key:alice")
	// Clear optional numeric/duration overrides so defaults apply.
	for _, k := range []string{
		"RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
		"ANCHOR_WORKER_INTERVAL", "ANCHOR_WORKER_BATCH", "ANCHOR_MAX_ATTEMPTS",
	} {
		t.Setenv(k, "")
	}
}

func TestLoad_Valid(t *testing.T) {
	setValidEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKeys["secret-key"] != "alice" {
		t.Errorf("API key identity = %q, want alice", cfg.APIKeys["secret-key"])
	}
	if cfg.RateLimitBurst != 40 {
		t.Errorf("RateLimitBurst default = %d, want 40", cfg.RateLimitBurst)
	}
	if cfg.DBMaxOpenConns != 25 {
		t.Errorf("DBMaxOpenConns default = %d, want 25", cfg.DBMaxOpenConns)
	}
	if cfg.AnchorWorkerInterval != 2*time.Second {
		t.Errorf("AnchorWorkerInterval default = %s, want 2s", cfg.AnchorWorkerInterval)
	}
}

func TestLoad_ValidWithOverrides(t *testing.T) {
	setValidEnv(t)
	t.Setenv("RATE_LIMIT_RPS", "5")
	t.Setenv("RATE_LIMIT_BURST", "9")
	t.Setenv("DB_MAX_OPEN_CONNS", "11")
	t.Setenv("ANCHOR_WORKER_INTERVAL", "750ms")
	t.Setenv("ANCHOR_MAX_ATTEMPTS", "3")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimitRPS != 5 {
		t.Errorf("RateLimitRPS = %v, want 5", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 9 {
		t.Errorf("RateLimitBurst = %d, want 9", cfg.RateLimitBurst)
	}
	if cfg.DBMaxOpenConns != 11 {
		t.Errorf("DBMaxOpenConns = %d, want 11", cfg.DBMaxOpenConns)
	}
	if cfg.AnchorWorkerInterval != 750*time.Millisecond {
		t.Errorf("AnchorWorkerInterval = %s, want 750ms", cfg.AnchorWorkerInterval)
	}
	if cfg.AnchorMaxAttempts != 3 {
		t.Errorf("AnchorMaxAttempts = %d, want 3", cfg.AnchorMaxAttempts)
	}
}

func TestLoad_Failures(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"missing database url", "DATABASE_URL", ""},
		{"missing s3 bucket", "S3_BUCKET", ""},
		{"missing api keys", "API_KEYS", ""},
		{"invalid from address", "FROM_ADDRESS", "0xnothex"},
		{"invalid contract address", "CONTRACT_ADDRESS", "not-an-address"},
		{"non-numeric rate limit burst", "RATE_LIMIT_BURST", "abc"},
		{"non-numeric rate limit rps", "RATE_LIMIT_RPS", "fast"},
		{"non-numeric max open conns", "DB_MAX_OPEN_CONNS", "lots"},
		{"non-numeric max idle conns", "DB_MAX_IDLE_CONNS", "few"},
		{"non-numeric obs ring capacity", "OBS_RING_CAPACITY", "big"},
		{"non-numeric anchor batch", "ANCHOR_WORKER_BATCH", "many"},
		{"non-numeric anchor max attempts", "ANCHOR_MAX_ATTEMPTS", "five"},
		{"bad duration", "ANCHOR_WORKER_INTERVAL", "soon"},
		{"bad conn lifetime duration", "DB_CONN_MAX_LIFETIME", "forever"},
		{"bad read timeout", "HTTP_READ_TIMEOUT", "quick"},
		{"bad read header timeout", "HTTP_READ_HEADER_TIMEOUT", "quick"},
		{"bad write timeout", "HTTP_WRITE_TIMEOUT", "slow"},
		{"bad idle timeout", "HTTP_IDLE_TIMEOUT", "idle"},
		{"missing s3 access key", "S3_ACCESS_KEY", ""},
		{"missing s3 secret key", "S3_SECRET_KEY", ""},
		{"missing rpc url", "RPC_URL", ""},
		{"missing contract address", "CONTRACT_ADDRESS", ""},
		{"non-hex char in 42-len from address", "FROM_ADDRESS", "0xgggggggggggggggggggggggggggggggggggggggg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(tc.key, tc.val)

			if _, err := config.Load(); err == nil {
				t.Fatalf("expected error when %s=%q", tc.key, tc.val)
			}
		})
	}
}

func TestLoad_APIKeyWithoutIdentityUsesKey(t *testing.T) {
	setValidEnv(t)
	// Mixed list: identity fallback, explicit identity, an empty entry, and an
	// entry with an empty key (":x") — both empties are skipped.
	t.Setenv("API_KEYS", "loose-key, k2:bob, ,:noident")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKeys["loose-key"] != "loose-key" {
		t.Errorf("identity = %q, want loose-key (fallback to key)", cfg.APIKeys["loose-key"])
	}
	if cfg.APIKeys["k2"] != "bob" {
		t.Errorf("identity = %q, want bob", cfg.APIKeys["k2"])
	}
	if len(cfg.APIKeys) != 2 {
		t.Errorf("expected 2 keys (empties skipped), got %d", len(cfg.APIKeys))
	}
}

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
