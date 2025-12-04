package unit_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"workflow/internal/documents"
	"workflow/internal/executor"
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
)

func TestWorkerProcessesAsyncJob(t *testing.T) {
	t.Parallel()

	queue := appworker.NewMemoryQueue(8)
	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	document, err := documentService.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "async.txt",
		ContentType: "text/plain",
		Content:     bytes.NewBufferString("async queue worker execution path"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "async-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "retrieve", Type: "retrieve_documents", Config: map[string]any{"document_ids": []string{document.ID}, "query_input_key": "query", "limit": 1}},
			{ID: "notify", Type: "http_tool", Config: map[string]any{"url": "https://api.example.com/notify", "method": "POST", "input_key": "query"}},
		},
		Edges: []workflow.Edge{
			{From: "retrieve", To: "notify"},
		},
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	httpClient := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		}, nil
	})
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	).WithHTTPClient(httpClient).WithJobQueue(queue)

	run, err := executorService.ExecuteAsync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"query": "worker"})
	if err != nil {
		t.Fatalf("execute async: %v", err)
	}
	if run.Status != executor.RunStatusQueued {
		t.Fatalf("expected queued run, got %q", run.Status)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workerService := appworker.NewService(logger, queue, executorService).WithPollTimeout(5 * time.Millisecond)

	processed, err := workerService.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("process next: %v", err)
	}
	if !processed {
		t.Fatal("expected worker to process a job")
	}

	resolvedRun, err := executorService.GetRun(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if resolvedRun.Status != executor.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", resolvedRun.Status)
	}
}

func TestWorkerRetriesFailedAsyncJob(t *testing.T) {
	t.Parallel()

	queue := appworker.NewMemoryQueue(8)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)

	attempts := 0
	httpClient := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		attempts++
		statusCode := http.StatusBadGateway
		body := `{"error":"retry"}`
		if attempts >= 2 {
			statusCode = http.StatusOK
			body = `{"status":"ok"}`
		}

		return &http.Response{
			StatusCode: statusCode,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "retry-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "notify", Type: "http_tool", Config: map[string]any{"url": "https://api.example.com/retry", "method": "POST", "input_key": "message"}},
		},
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		executor.NewMockLLMProvider(),
	).WithHTTPClient(httpClient).WithJobQueue(queue)

	run, err := executorService.ExecuteAsync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"message": "retry"})
	if err != nil {
		t.Fatalf("execute async: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workerService := appworker.NewService(logger, queue, executorService).WithPollTimeout(5 * time.Millisecond)

	processed, err := workerService.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("first process next: %v", err)
	}
	if !processed {
		t.Fatal("expected first async job to process")
	}

	time.Sleep(80 * time.Millisecond)

	processed, err = workerService.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("second process next: %v", err)
	}
	if !processed {
		t.Fatal("expected retried async job to process")
	}

	resolvedRun, err := executorService.GetRun(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if resolvedRun.Status != executor.RunStatusCompleted {
		t.Fatalf("expected completed run after retry, got %q", resolvedRun.Status)
	}
	if resolvedRun.Attempt != 2 {
		t.Fatalf("expected attempt count 2, got %d", resolvedRun.Attempt)
	}
}
