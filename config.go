package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr string

	TinfoilAPIKey          string
	SafeguardModel         string
	SafeguardReviewModel   string
	SafeguardPolicy        string
	SafeguardTimeout       time.Duration
	SafeguardReviewTimeout time.Duration
	MaxTranscriptBytes     int

	MaxRequestBytes int

	QueueTTL     time.Duration
	QueueMaxSize int
	Workers      int

	ControlPlaneURL string
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:           envOr("LISTEN_ADDR", ":8090"),
		SafeguardModel:       envOr("SAFEGUARD_MODEL", "gpt-oss-safeguard-120b"),
		SafeguardReviewModel: envOr("SAFEGUARD_REVIEW_MODEL", "kimi-k3"),
		ControlPlaneURL:      envOr("CONTROL_PLANE_URL", "https://api.tinfoil.sh"),
	}

	var err error
	if cfg.TinfoilAPIKey, err = requireEnv("TINFOIL_API_KEY"); err != nil {
		return nil, err
	}
	if cfg.SafeguardPolicy, err = requireEnv("SAFEGUARD_POLICY"); err != nil {
		return nil, err
	}
	if cfg.SafeguardTimeout, err = positiveEnv("SAFEGUARD_TIMEOUT", 5*time.Minute, time.ParseDuration); err != nil {
		return nil, err
	}
	if cfg.SafeguardReviewTimeout, err = positiveEnv("SAFEGUARD_REVIEW_TIMEOUT", 10*time.Minute, time.ParseDuration); err != nil {
		return nil, err
	}
	if cfg.MaxTranscriptBytes, err = positiveEnv("MAX_TRANSCRIPT_BYTES", 320_000, strconv.Atoi); err != nil {
		return nil, err
	}
	if cfg.MaxRequestBytes, err = positiveEnv("MAX_REQUEST_BYTES", 4<<20, strconv.Atoi); err != nil {
		return nil, err
	}
	if cfg.QueueTTL, err = positiveEnv("QUEUE_TTL", time.Hour, time.ParseDuration); err != nil {
		return nil, err
	}
	if cfg.QueueMaxSize, err = positiveEnv("QUEUE_MAX_SIZE", 10_000, strconv.Atoi); err != nil {
		return nil, err
	}
	if cfg.Workers, err = positiveEnv("WORKERS", 4, strconv.Atoi); err != nil {
		return nil, err
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return val, nil
}

// positiveEnv parses key with parse when it is set, otherwise returns fallback.
// Both must be strictly positive.
func positiveEnv[T int | time.Duration](key string, fallback T, parse func(string) (T, error)) (T, error) {
	val := fallback
	if raw := os.Getenv(key); raw != "" {
		parsed, err := parse(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		val = parsed
	}
	if val <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return val, nil
}
