package unit_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

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

	fixture := newExecutorFixture(t)
	document := fixture.createDocument(t, "tenant-a", "tracing.txt", "distributed tracing for workflow execution")
	createdWorkflow := fixture.createWorkflow(t, "tenant-a", workflow.Definition{
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

	run, err := fixture.executor.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"query": "tracing"})
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
