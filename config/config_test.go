package config

import (
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Setenv("TINFOIL_API_KEY", "k")
	t.Setenv("SAFEGUARD_POLICY", "p")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QueueTTL != time.Hour || cfg.Workers != 4 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoad_RequiredSecrets(t *testing.T) {
	setRequired(t)
	t.Setenv("TINFOIL_API_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing TINFOIL_API_KEY")
	}
}

func TestLoad_RejectsInvalidValues(t *testing.T) {
	for key, value := range map[string]string{
		"QUEUE_TTL":         "soon",
		"QUEUE_MAX_SIZE":    "0",
		"WORKERS":           "-1",
		"MAX_REQUEST_BYTES": "big",
		"SAFEGUARD_TIMEOUT": "0s",
	} {
		setRequired(t)
		t.Setenv(key, value)
		if _, err := Load(); err == nil {
			t.Errorf("%s=%s: expected error", key, value)
		}
		t.Setenv(key, "")
	}
}
