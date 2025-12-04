package integration_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	appapi "workflow/internal/api"
	"workflow/internal/audit"
	"workflow/internal/auth"
	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/platform/health"
	"workflow/internal/tenant"
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
)

const (
	exampleTenantAKey = "example-key-tenant-a"
	exampleTenantBKey = "example-key-tenant-b"
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

type testAppOptions struct {
	HTTPClient    executor.HTTPClient
	TriggerLimit  int
	TriggerWindow time.Duration
}

func newTestApp(t *testing.T, httpClient executor.HTTPClient) testApp {
	return newTestAppWithOptions(t, testAppOptions{
		HTTPClient:    httpClient,
		TriggerLimit:  20,
		TriggerWindow: time.Minute,
	})
}

func newTestAppWithOptions(t *testing.T, options testAppOptions) testApp {
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
			Plaintext: exampleTenantAKey,
			Status:    auth.StatusActive,
		},
		{
			ID:        "key-b",
			TenantID:  "tenant-b",
			Label:     "bootstrap",
			Plaintext: exampleTenantBKey,
			Status:    auth.StatusActive,
		},
	}), tenantService)
	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	triggerControl := tenant.NewTriggerControl(options.TriggerLimit, options.TriggerWindow)
	auditService := audit.NewService(audit.NewMemoryRepository())
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	queue := appworker.NewMemoryQueue(32)
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	).WithHTTPClient(options.HTTPClient).WithJobQueue(queue).WithAudit(auditService).WithTriggerControl(triggerControl)
	workerService := appworker.NewService(logger, queue, executorService).WithPollTimeout(5 * time.Millisecond)

	return testApp{
		Handler: appapi.NewHandler(logger, healthService, authService, documentService, workflowService, executorService, auditService),
		Worker:  workerService,
	}
}

func drainAsyncJob(t *testing.T, app testApp) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for {
		processed, err := app.Worker.ProcessNext(ctx)
		if err != nil {
			t.Fatalf("process async job: %v", err)
		}
		if processed {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("expected async job to be processed")
		}
	}
}
