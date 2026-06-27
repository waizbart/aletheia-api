package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
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

// Config is the fully parsed and validated application configuration. Load reads
// it from the environment once at startup so the process fails fast (with a clear
// message) on a bad config instead of panicking deep in a request.
type Config struct {
	DatabaseURL string

	ServerPort         string
	CORSAllowedOrigins []string

	// APIKeys maps an API key to the registrant identity it authenticates as.
	APIKeys        map[string]string
	RateLimitRPS   float64
	RateLimitBurst int

	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	HTTPReadTimeout       time.Duration
	HTTPReadHeaderTimeout time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration

	RPCURL          string
	FromAddress     string
	ContractAddress string

	S3Endpoint  string
	S3Bucket    string
	S3Region    string
	S3AccessKey string
	S3SecretKey string

	OTELEndpoint    string
	OTELServiceName string
	ObsRingCapacity int

	AnchorWorkerInterval time.Duration
	AnchorWorkerBatch    int
	AnchorMaxAttempts    int
}

// Load parses and validates configuration from the environment. It returns an
// error (rather than calling Fatalf) so the caller controls process exit and the
// behavior is unit-testable.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		ServerPort:         EnvOrDefault("SERVER_PORT", "8080"),
		CORSAllowedOrigins: strings.Split(EnvOrDefault("CORS_ALLOWED_ORIGINS", "*"), ","),
		RPCURL:             os.Getenv("RPC_URL"),
		FromAddress:        os.Getenv("FROM_ADDRESS"),
		ContractAddress:    os.Getenv("CONTRACT_ADDRESS"),
		S3Endpoint:         EnvOrDefault("S3_ENDPOINT", ""),
		S3Bucket:           os.Getenv("S3_BUCKET"),
		S3Region:           EnvOrDefault("S3_REGION", "us-east-1"),
		S3AccessKey:        os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:        os.Getenv("S3_SECRET_KEY"),
		OTELEndpoint:       EnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTELServiceName:    EnvOrDefault("OTEL_SERVICE_NAME", "aletheia-api"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
		return nil, fmt.Errorf("S3_BUCKET, S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	if c.RPCURL == "" || c.FromAddress == "" || c.ContractAddress == "" {
		return nil, fmt.Errorf("RPC_URL, FROM_ADDRESS and CONTRACT_ADDRESS are required")
	}
	if !isHexAddress(c.FromAddress) {
		return nil, fmt.Errorf("FROM_ADDRESS is not a valid 0x address: %q", c.FromAddress)
	}
	if !isHexAddress(c.ContractAddress) {
		return nil, fmt.Errorf("CONTRACT_ADDRESS is not a valid 0x address: %q", c.ContractAddress)
	}

	keys, err := parseAPIKeys(os.Getenv("API_KEYS"))
	if err != nil {
		return nil, err
	}
	c.APIKeys = keys

	if c.RateLimitRPS, err = parseFloat("RATE_LIMIT_RPS", 20); err != nil {
		return nil, err
	}
	if c.RateLimitBurst, err = parseInt("RATE_LIMIT_BURST", 40); err != nil {
		return nil, err
	}
	if c.DBMaxOpenConns, err = parseInt("DB_MAX_OPEN_CONNS", 25); err != nil {
		return nil, err
	}
	if c.DBMaxIdleConns, err = parseInt("DB_MAX_IDLE_CONNS", 5); err != nil {
		return nil, err
	}
	if c.DBConnMaxLifetime, err = parseDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute); err != nil {
		return nil, err
	}
	if c.HTTPReadTimeout, err = parseDuration("HTTP_READ_TIMEOUT", 30*time.Second); err != nil {
		return nil, err
	}
	if c.HTTPReadHeaderTimeout, err = parseDuration("HTTP_READ_HEADER_TIMEOUT", 10*time.Second); err != nil {
		return nil, err
	}
	if c.HTTPWriteTimeout, err = parseDuration("HTTP_WRITE_TIMEOUT", 120*time.Second); err != nil {
		return nil, err
	}
	if c.HTTPIdleTimeout, err = parseDuration("HTTP_IDLE_TIMEOUT", 120*time.Second); err != nil {
		return nil, err
	}
	if c.ObsRingCapacity, err = parseInt("OBS_RING_CAPACITY", 50); err != nil {
		return nil, err
	}
	if c.AnchorWorkerInterval, err = parseDuration("ANCHOR_WORKER_INTERVAL", 2*time.Second); err != nil {
		return nil, err
	}
	if c.AnchorWorkerBatch, err = parseInt("ANCHOR_WORKER_BATCH", 16); err != nil {
		return nil, err
	}
	if c.AnchorMaxAttempts, err = parseInt("ANCHOR_MAX_ATTEMPTS", 5); err != nil {
		return nil, err
	}

	return c, nil
}

// parseAPIKeys reads "key:identity,key2:identity2". An entry without a colon uses
// the key itself as the identity. At least one key is required.
func parseAPIKeys(raw string) (map[string]string, error) {
	keys := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, identity, found := strings.Cut(entry, ":")
		key = strings.TrimSpace(key)
		identity = strings.TrimSpace(identity)
		if key == "" {
			continue
		}
		if !found || identity == "" {
			identity = key
		}
		keys[key] = identity
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("API_KEYS must define at least one key (format: \"key:identity,...\")")
	}
	return keys, nil
}

func parseInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func parseFloat(key string, fallback float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return f, nil
}

func parseDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration (e.g. 30s, 2m): %w", key, err)
	}
	return d, nil
}

// isHexAddress reports whether v is a 0x-prefixed 20-byte hex string.
func isHexAddress(v string) bool {
	if len(v) != 42 || !strings.HasPrefix(v, "0x") {
		return false
	}
	for _, c := range v[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
