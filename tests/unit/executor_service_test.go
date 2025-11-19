package unit_test

import (
	"bytes"
	"context"
	"testing"

	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/workflow"
)

func TestExecuteSyncCompletesLinearWorkflow(t *testing.T) {
	t.Parallel()

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	document, err := documentService.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "architecture.txt",
		ContentType: "text/plain",
		Content:     bytes.NewBufferString("backend orchestration platform architecture reliability"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "sync-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "retrieve", Type: "retrieve_documents", Config: map[string]any{"document_ids": []string{document.ID}, "query_input_key": "query", "limit": 2}},
			{ID: "summarize", Type: "llm", Config: map[string]any{"prompt": "Summarize", "input_key": "query", "context_step": "retrieve"}},
			{ID: "audit", Type: "audit_log", Config: map[string]any{"message": "done", "from_step": "summarize"}},
		},
		Edges: []workflow.Edge{
			{From: "retrieve", To: "summarize"},
			{From: "summarize", To: "audit"},
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

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"query": "backend"})
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	if run.Status != executor.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}

	steps, err := executorService.ListSteps(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[1].NodeType != "llm" {
		t.Fatalf("expected llm step, got %q", steps[1].NodeType)
	}
}

func TestExecuteSyncMarksUnsupportedNodesFailed(t *testing.T) {
	t.Parallel()

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "approval-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "approval", Type: "approval"},
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

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, nil)
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	if run.Status != executor.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
	if run.Error == "" {
		t.Fatal("expected run error to be recorded")
	}
}
