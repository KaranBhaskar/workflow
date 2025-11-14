package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	ServiceName     string
	AppEnv          string
	HTTP            HTTPConfig
	Auth            AuthConfig
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
