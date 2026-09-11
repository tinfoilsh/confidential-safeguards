package main

import (
	"reflect"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Setenv("TINFOIL_API_KEY", "k")
	t.Setenv("SAFEGUARD_POLICY", "p")
}

func TestLoadConfig_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		ListenAddr:             ":8090",
		TinfoilAPIKey:          "k",
		SafeguardModel:         "gpt-oss-safeguard-120b",
		SafeguardReviewModel:   "kimi-k3",
		SafeguardPolicy:        "p",
		SafeguardTimeout:       5 * time.Minute,
		SafeguardReviewTimeout: 10 * time.Minute,
		MaxTranscriptBytes:     320_000,
		MaxRequestBytes:        4 << 20,
		QueueTTL:               time.Hour,
		QueueMaxSize:           10_000,
		Workers:                4,
		ControlPlaneURL:        "https://api.tinfoil.sh",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("defaults = %+v\nwant %+v", cfg, want)
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	setRequired(t)
	t.Setenv("SAFEGUARD_TIMEOUT", "90s")
	t.Setenv("MAX_REQUEST_BYTES", "1024")
	t.Setenv("WORKERS", "12")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SafeguardTimeout != 90*time.Second || cfg.MaxRequestBytes != 1024 || cfg.Workers != 12 {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestLoadConfig_RequiredValues(t *testing.T) {
	for _, key := range []string{"TINFOIL_API_KEY", "SAFEGUARD_POLICY"} {
		setRequired(t)
		t.Setenv(key, "")
		if _, err := LoadConfig(); err == nil {
			t.Errorf("expected error for missing %s", key)
		}
	}
}

func TestLoadConfig_RejectsInvalidValues(t *testing.T) {
	setRequired(t)
	for key, value := range map[string]string{
		"QUEUE_TTL":                "soon",
		"QUEUE_MAX_SIZE":           "0",
		"WORKERS":                  "-1",
		"MAX_REQUEST_BYTES":        "big",
		"MAX_TRANSCRIPT_BYTES":     "0",
		"SAFEGUARD_TIMEOUT":        "0s",
		"SAFEGUARD_REVIEW_TIMEOUT": "-1m",
	} {
		t.Setenv(key, value)
		if _, err := LoadConfig(); err == nil {
			t.Errorf("%s=%s: expected error", key, value)
		}
		t.Setenv(key, "")
	}
}
