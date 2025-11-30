package integration_test

import (
	"context"
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
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
)

type stubHTTPClient func(*http.Request) (*http.Response, error)

func (f stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newAuthenticatedHandler(t *testing.T) http.Handler {
	return newTestApp(t, nil).Handler
}

func newAuthenticatedHandlerWithHTTPClient(t *testing.T, httpClient executor.HTTPClient) http.Handler {
	return newTestApp(t, httpClient).Handler
}

type testApp struct {
	Handler http.Handler
	Worker  *appworker.Service
}

func newTestApp(t *testing.T, httpClient executor.HTTPClient) testApp {
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
	queue := appworker.NewMemoryQueue(32)
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	).WithHTTPClient(httpClient).WithJobQueue(queue)
	workerService := appworker.NewService(logger, queue, executorService).WithPollTimeout(5 * time.Millisecond)

	return testApp{
		Handler: appapi.NewHandler(logger, healthService, authService, documentService, workflowService, executorService),
		Worker:  workerService,
	}
}

func drainAsyncJob(t *testing.T, app testApp) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	processed, err := app.Worker.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("process async job: %v", err)
	}
	if !processed {
		t.Fatal("expected async job to be processed")
	}
}
