package unit_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"workflow/internal/executor"
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
)

func TestWorkerProcessesAsyncJob(t *testing.T) {
	t.Parallel()

	queue := appworker.NewMemoryQueue(8)
	httpClient := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		}, nil
	})
	fixture := newExecutorFixture(t, withHTTPClient(httpClient), withJobQueue(queue))
	document := fixture.createDocument(t, "tenant-a", "async.txt", "async queue worker execution path")
	createdWorkflow := fixture.createWorkflow(t, "tenant-a", workflow.Definition{
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

	run, err := fixture.executor.ExecuteAsync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"query": "worker"})
	if err != nil {
		t.Fatalf("execute async: %v", err)
	}
	if run.Status != executor.RunStatusQueued {
		t.Fatalf("expected queued run, got %q", run.Status)
	}

	workerService := appworker.NewService(discardLogger(), queue, fixture.executor).WithPollTimeout(5 * time.Millisecond)

	processed, err := workerService.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("process next: %v", err)
	}
	if !processed {
		t.Fatal("expected worker to process a job")
	}

	resolvedRun, err := fixture.executor.GetRun(context.Background(), "tenant-a", run.ID)
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

	fixture := newExecutorFixture(t, withHTTPClient(httpClient), withJobQueue(queue))
	createdWorkflow := fixture.createWorkflow(t, "tenant-a", workflow.Definition{
		Name:    "retry-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "notify", Type: "http_tool", Config: map[string]any{"url": "https://api.example.com/retry", "method": "POST", "input_key": "message"}},
		},
	})

	run, err := fixture.executor.ExecuteAsync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"message": "retry"})
	if err != nil {
		t.Fatalf("execute async: %v", err)
	}

	workerService := appworker.NewService(discardLogger(), queue, fixture.executor).WithPollTimeout(5 * time.Millisecond)

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

	resolvedRun, err := fixture.executor.GetRun(context.Background(), "tenant-a", run.ID)
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
