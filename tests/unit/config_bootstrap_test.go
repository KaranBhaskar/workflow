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

func TestLoadParsesS3StorageConfig(t *testing.T) {
	t.Setenv("MINIO_ENDPOINT", "minio:9000")
	t.Setenv("MINIO_ACCESS_KEY", "example-minio-user")
	t.Setenv("MINIO_SECRET_KEY", "example-minio-password")
	t.Setenv("MINIO_BUCKET", "workflow")
	t.Setenv("MINIO_USE_SSL", "true")

	cfg, err := config.Load("workflow-api")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Storage.S3.Endpoint != "minio:9000" {
		t.Fatalf("expected minio endpoint, got %q", cfg.Storage.S3.Endpoint)
	}
	if cfg.Storage.S3.AccessKey != "example-minio-user" {
		t.Fatalf("expected minio access key, got %q", cfg.Storage.S3.AccessKey)
	}
	if cfg.Storage.S3.SecretKey != "example-minio-password" {
		t.Fatalf("expected minio secret key, got %q", cfg.Storage.S3.SecretKey)
	}
	if cfg.Storage.S3.Bucket != "workflow" {
		t.Fatalf("expected workflow bucket, got %q", cfg.Storage.S3.Bucket)
	}
	if !cfg.Storage.S3.UseSSL {
		t.Fatal("expected minio ssl flag to be true")
	}
}
