package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"workflow/internal/api"
	"workflow/internal/platform/config"
	"workflow/internal/platform/health"
	"workflow/internal/platform/logging"
)

func main() {
	cfg, err := config.Load("workflow-api")
	if err != nil {
		panic(err)
	}

	logger := logging.New(cfg.ServiceName, cfg.AppEnv)
	healthService := health.NewService(
		cfg.ServiceName,
		cfg.Dependencies.ReadinessTimeout,
		buildDependencyCheckers(cfg)...,
	)

	server := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      api.NewHandler(logger, healthService),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api server starting", "addr", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("api server stopped unexpectedly", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shut down api server cleanly", "error", err)
		os.Exit(1)
	}

	logger.Info("api server stopped")
}

func buildDependencyCheckers(cfg config.Config) []health.Checker {
	checkers := make([]health.Checker, 0, 3)

	if cfg.Dependencies.PostgresAddr != "" {
		checkers = append(checkers, health.NewTCPChecker("postgres", cfg.Dependencies.PostgresAddr))
	}

	if cfg.Dependencies.RedisAddr != "" {
		checkers = append(checkers, health.NewTCPChecker("redis", cfg.Dependencies.RedisAddr))
	}

	if cfg.Dependencies.MinIOAddr != "" {
		checkers = append(checkers, health.NewTCPChecker("minio", cfg.Dependencies.MinIOAddr))
	}

	return checkers
}
