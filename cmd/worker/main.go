package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"workflow/internal/platform/config"
	"workflow/internal/platform/logging"
	"workflow/internal/platform/observability"
)

func main() {
	cfg, err := config.Load("workflow-worker")
	if err != nil {
		panic(err)
	}

	logger := logging.New(cfg.ServiceName, cfg.AppEnv)
	shutdownObservability, err := observability.Setup(context.Background(), cfg)
	if err != nil {
		logger.Error("failed to initialize observability", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdownObservability(context.Background()); err != nil {
			logger.Error("failed to shut down observability", "error", err)
		}
	}()
	logger.Info(
		"worker bootstrap ready",
		"redis_addr", cfg.Dependencies.RedisAddr,
		"status", "queue execution will be added in a later commit",
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("worker stopped")
}
