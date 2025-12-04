package unit_test

import (
	"bytes"
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/workflow"
)

func TestExecuteSyncEmitsTracingSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider()
	provider.RegisterSpanProcessor(recorder)
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = provider.Shutdown(context.Background())
	})

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	document, err := documentService.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "tracing.txt",
		ContentType: "text/plain",
		Content:     bytes.NewBufferString("distributed tracing for workflow execution"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "trace-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "retrieve", Type: "retrieve_documents", Config: map[string]any{"document_ids": []string{document.ID}, "query_input_key": "query", "limit": 1}},
			{ID: "summarize", Type: "llm", Config: map[string]any{"prompt": "Summarize", "input_key": "query", "context_step": "retrieve"}},
		},
		Edges: []workflow.Edge{
			{From: "retrieve", To: "summarize"},
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
	)

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"query": "tracing"})
	if err != nil {
		t.Fatalf("execute sync workflow: %v", err)
	}
	if run.Status != executor.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}

	var names []string
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}

	for _, expected := range []string{"workflow.run.sync", "workflow.run.execute", "workflow.step"} {
		if !containsString(names, expected) {
			t.Fatalf("expected span %q in %v", expected, names)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}
