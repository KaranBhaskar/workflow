package logging

import (
	"log/slog"
	"os"
)

func New(serviceName, environment string) *slog.Logger {
	return slog.New(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
	).With(
		"service", serviceName,
		"environment", environment,
	)
}
