package unit_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/workflow"
)

type executorFixture struct {
	documents *documents.Service
	workflows *workflow.Service
	executor  *executor.Service
}

type executorFixtureOption func(*executorFixture)

func newExecutorFixture(t *testing.T, options ...executorFixtureOption) executorFixture {
	t.Helper()

	fixture := executorFixture{
		documents: documents.NewService(
			documents.NewMemoryRepository(),
			documents.NewLocalObjectStore(t.TempDir()),
		),
		workflows: workflow.NewService(workflow.NewMemoryRepository()),
	}
	fixture.executor = executor.NewService(
		executor.NewMemoryRepository(),
		fixture.workflows,
		fixture.documents,
		executor.NewMockLLMProvider(),
	)

	for _, option := range options {
		option(&fixture)
	}

	return fixture
}

func withHTTPClient(client executor.HTTPClient) executorFixtureOption {
	return func(fixture *executorFixture) {
		fixture.executor = fixture.executor.WithHTTPClient(client)
	}
}

func withJobQueue(queue executor.JobQueue) executorFixtureOption {
	return func(fixture *executorFixture) {
		fixture.executor = fixture.executor.WithJobQueue(queue)
	}
}

func withWorkflowRepository(repository workflow.Repository) executorFixtureOption {
	return func(fixture *executorFixture) {
		fixture.workflows = workflow.NewService(repository)
		fixture.executor = executor.NewService(
			executor.NewMemoryRepository(),
			fixture.workflows,
			fixture.documents,
			executor.NewMockLLMProvider(),
		)
	}
}

func (fixture executorFixture) createDocument(t *testing.T, tenantID, filename, content string) documents.Document {
	t.Helper()

	document, err := fixture.documents.Create(context.Background(), tenantID, documents.CreateParams{
		Filename:    filename,
		ContentType: "text/plain",
		Content:     bytes.NewBufferString(content),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	return document
}

func (fixture executorFixture) createWorkflow(t *testing.T, tenantID string, definition workflow.Definition) workflow.Workflow {
	t.Helper()

	createdWorkflow, _, err := fixture.workflows.Create(context.Background(), tenantID, definition)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	return createdWorkflow
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
