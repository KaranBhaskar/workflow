package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServiceName     string
	AppEnv          string
	HTTP            HTTPConfig
	Auth            AuthConfig
	Limits          LimitsConfig
	Observability   ObservabilityConfig
	Storage         StorageConfig
	Dependencies    DependencyConfig
	ShutdownTimeout time.Duration
}

type HTTPConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type DependencyConfig struct {
	PostgresAddr     string
	RedisAddr        string
	MinIOAddr        string
	ReadinessTimeout time.Duration
}

type StorageConfig struct {
	ObjectDir string
	S3        S3StorageConfig
}

type S3StorageConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

type LimitsConfig struct {
	TenantExecuteLimit  int
	TenantExecuteWindow time.Duration
}

type ObservabilityConfig struct {
	Exporter    string
	SampleRatio float64
}

func Load(defaultServiceName string) (Config, error) {
	readTimeout, err := durationEnv("HTTP_READ_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}

	writeTimeout, err := durationEnv("HTTP_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	idleTimeout, err := durationEnv("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	readinessTimeout, err := durationEnv("READINESS_TIMEOUT", 750*time.Millisecond)
	if err != nil {
		return Config{}, err
	}

	executeWindow, err := durationEnv("TENANT_EXECUTE_WINDOW", time.Minute)
	if err != nil {
		return Config{}, err
	}

	authConfig, err := bootstrapAuthConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		ServiceName: stringEnv("SERVICE_NAME", defaultServiceName),
		AppEnv:      stringEnv("APP_ENV", "development"),
		HTTP: HTTPConfig{
			Addr:         stringEnv("API_ADDR", ":8080"),
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
			IdleTimeout:  idleTimeout,
		},
		Dependencies: DependencyConfig{
			PostgresAddr:     os.Getenv("POSTGRES_ADDR"),
			RedisAddr:        os.Getenv("REDIS_ADDR"),
			MinIOAddr:        os.Getenv("MINIO_ADDR"),
			ReadinessTimeout: readinessTimeout,
		},
		Storage: StorageConfig{
			ObjectDir: stringEnv("OBJECT_STORAGE_DIR", filepath.Join(os.TempDir(), "workflow-objects")),
			S3: S3StorageConfig{
				Endpoint:  strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")),
				AccessKey: strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY")),
				SecretKey: strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY")),
				Bucket:    strings.TrimSpace(os.Getenv("MINIO_BUCKET")),
				UseSSL:    boolEnv("MINIO_USE_SSL", false),
			},
		},
		Limits: LimitsConfig{
			TenantExecuteLimit:  intEnv("TENANT_EXECUTE_LIMIT", 20),
			TenantExecuteWindow: executeWindow,
		},
		Observability: ObservabilityConfig{
			Exporter:    stringEnv("OTEL_EXPORTER", "none"),
			SampleRatio: floatEnv("OTEL_SAMPLE_RATIO", 1.0),
		},
		Auth:            authConfig,
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

func stringEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return parsed, nil
}

func intEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func floatEnv(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}

	return parsed
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}

	return parsed
}
