package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"workflow/internal/api"
	"workflow/internal/audit"
	"workflow/internal/auth"
	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/platform/config"
	"workflow/internal/platform/health"
	"workflow/internal/platform/logging"
	"workflow/internal/tenant"
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
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
	tenantService := tenant.NewService(tenant.NewMemoryRepository(buildBootstrapTenants(cfg.Auth.BootstrapAPIKeys)))
	authService := auth.NewService(
		auth.NewMemoryRepository(buildBootstrapAPIKeys(cfg.Auth.BootstrapAPIKeys)),
		tenantService,
	)
	auditService := audit.NewService(audit.NewMemoryRepository())
	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(cfg.Storage.ObjectDir),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	jobQueue := buildJobQueue(cfg)
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	).WithJobQueue(jobQueue).WithAudit(auditService)
	backgroundWorker := appworker.NewService(logger, jobQueue, executorService)

	server := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      api.NewHandler(logger, healthService, authService, documentService, workflowService, executorService, auditService),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := backgroundWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
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

func buildJobQueue(cfg config.Config) executor.JobQueue {
	if cfg.Dependencies.RedisAddr != "" {
		return appworker.NewRedisQueue(cfg.Dependencies.RedisAddr, "")
	}

	return appworker.NewMemoryQueue(128)
}

func buildBootstrapTenants(entries []config.BootstrapAPIKey) []tenant.Tenant {
	seen := make(map[string]struct{}, len(entries))
	tenants := make([]tenant.Tenant, 0, len(entries))

	for _, entry := range entries {
		if _, exists := seen[entry.TenantID]; exists {
			continue
		}

		seen[entry.TenantID] = struct{}{}
		tenants = append(tenants, tenant.Tenant{
			ID:     entry.TenantID,
			Name:   entry.TenantName,
			Status: tenant.StatusActive,
		})
	}

	return tenants
}

func buildBootstrapAPIKeys(entries []config.BootstrapAPIKey) []auth.APIKey {
	keys := make([]auth.APIKey, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, auth.APIKey{
			ID:        entry.APIKeyID,
			TenantID:  entry.TenantID,
			Label:     entry.Label,
			Plaintext: entry.PlaintextKey,
			Status:    auth.StatusActive,
		})
	}

	return keys
}
