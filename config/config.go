package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr string

	TinfoilAPIKey      string
	SafeguardModel     string
	SafeguardPolicy    string
	SafeguardTimeout   time.Duration
	MaxTranscriptBytes int

	MaxRequestBytes int64

	QueueTTL     time.Duration
	QueueMaxSize int
	Workers      int

	ControlPlaneURL    string
	ControlPlaneSecret string
}

func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:         getEnv("LISTEN_ADDR", ":8090"),
		TinfoilAPIKey:      os.Getenv("TINFOIL_API_KEY"),
		SafeguardModel:     getEnv("SAFEGUARD_MODEL", "gpt-oss-safeguard-120b"),
		SafeguardPolicy:    os.Getenv("SAFEGUARD_POLICY"),
		ControlPlaneURL:    getEnv("CONTROL_PLANE_URL", "https://api.tinfoil.sh"),
		ControlPlaneSecret: os.Getenv("CONTROL_PLANE_SECRET"),
	}

	var err error
	if cfg.SafeguardTimeout, err = getEnvDuration("SAFEGUARD_TIMEOUT", 5*time.Minute); err != nil {
		return nil, err
	}
	if cfg.MaxTranscriptBytes, err = getEnvInt("MAX_TRANSCRIPT_BYTES", 320_000); err != nil {
		return nil, err
	}
	if cfg.MaxRequestBytes, err = getEnvInt64("MAX_REQUEST_BYTES", 4<<20); err != nil {
		return nil, err
	}
	if cfg.QueueTTL, err = getEnvDuration("QUEUE_TTL", time.Hour); err != nil {
		return nil, err
	}
	if cfg.QueueMaxSize, err = getEnvInt("QUEUE_MAX_SIZE", 10_000); err != nil {
		return nil, err
	}
	if cfg.Workers, err = getEnvInt("WORKERS", 4); err != nil {
		return nil, err
	}

	for name, value := range map[string]string{
		"TINFOIL_API_KEY":      cfg.TinfoilAPIKey,
		"SAFEGUARD_POLICY":     cfg.SafeguardPolicy,
		"CONTROL_PLANE_SECRET": cfg.ControlPlaneSecret,
	} {
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	for name, value := range map[string]int64{
		"SAFEGUARD_TIMEOUT":    int64(cfg.SafeguardTimeout),
		"MAX_TRANSCRIPT_BYTES": int64(cfg.MaxTranscriptBytes),
		"MAX_REQUEST_BYTES":    cfg.MaxRequestBytes,
		"QUEUE_TTL":            int64(cfg.QueueTTL),
		"QUEUE_MAX_SIZE":       int64(cfg.QueueMaxSize),
		"WORKERS":              int64(cfg.Workers),
	} {
		if value <= 0 {
			return nil, fmt.Errorf("%s must be positive", name)
		}
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func getEnvInt64(key string, fallback int64) (int64, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}
