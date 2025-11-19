package integration_test

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	appapi "workflow/internal/api"
	"workflow/internal/auth"
	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/platform/health"
	"workflow/internal/tenant"
	"workflow/internal/workflow"
)

func newAuthenticatedHandler(t *testing.T) http.Handler {
	t.Helper()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	healthService := health.NewService("workflow-api", time.Second)
	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusActive},
		{ID: "tenant-b", Name: "Tenant B", Status: tenant.StatusActive},
	}))
	authService := auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "key-a",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "dev-key-tenant-a",
			Status:    auth.StatusActive,
		},
		{
			ID:        "key-b",
			TenantID:  "tenant-b",
			Label:     "bootstrap",
			Plaintext: "dev-key-tenant-b",
			Status:    auth.StatusActive,
		},
	}), tenantService)
	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	)

	return appapi.NewHandler(logger, healthService, authService, documentService, workflowService, executorService)
}
