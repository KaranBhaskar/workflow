package unit_test

import (
	"testing"

	"workflow/internal/platform/config"
)

func TestLoadParsesBootstrapAPIKeys(t *testing.T) {
	t.Setenv("BOOTSTRAP_API_KEYS", "tenant-a|Tenant A|key-a,tenant-b|Tenant B|key-b")

	cfg, err := config.Load("workflow-api")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Auth.BootstrapAPIKeys) != 2 {
		t.Fatalf("expected 2 bootstrap keys, got %d", len(cfg.Auth.BootstrapAPIKeys))
	}

	if cfg.Auth.BootstrapAPIKeys[0].TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", cfg.Auth.BootstrapAPIKeys[0].TenantID)
	}
}

func TestLoadRejectsMalformedBootstrapAPIKeys(t *testing.T) {
	t.Setenv("BOOTSTRAP_API_KEYS", "tenant-a|Tenant A")

	if _, err := config.Load("workflow-api"); err == nil {
		t.Fatal("expected malformed bootstrap auth config to fail")
	}
}

func TestLoadParsesObservabilityConfig(t *testing.T) {
	t.Setenv("OTEL_EXPORTER", "stdout")
	t.Setenv("OTEL_SAMPLE_RATIO", "0.25")

	cfg, err := config.Load("workflow-api")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Observability.Exporter != "stdout" {
		t.Fatalf("expected stdout exporter, got %q", cfg.Observability.Exporter)
	}
	if cfg.Observability.SampleRatio != 0.25 {
		t.Fatalf("expected sample ratio 0.25, got %v", cfg.Observability.SampleRatio)
	}
}
