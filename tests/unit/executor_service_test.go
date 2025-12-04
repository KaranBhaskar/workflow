package unit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/workflow"
)

type stubHTTPClient func(*http.Request) (*http.Response, error)

func (f stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

func TestExecuteSyncPausesApprovalWorkflow(t *testing.T) {
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
			{ID: "review", Type: "approval", Config: map[string]any{"message": "manager approval required"}},
			{ID: "audit", Type: "audit_log", Config: map[string]any{"message": "approved"}},
		},
		Edges: []workflow.Edge{
			{From: "review", To: "audit"},
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
	if run.Status != executor.RunStatusWaitingApproval {
		t.Fatalf("expected waiting approval run, got %q", run.Status)
	}

	steps, err := executorService.ListSteps(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step before resume, got %d", len(steps))
	}
	if steps[0].Status != executor.StepStatusWaitingApproval {
		t.Fatalf("expected waiting approval step, got %q", steps[0].Status)
	}

	resumedRun, err := executorService.ResumeApproval(context.Background(), "tenant-a", run.ID, true, "approved", nil)
	if err != nil {
		t.Fatalf("resume approval: %v", err)
	}
	if resumedRun.Status != executor.RunStatusCompleted {
		t.Fatalf("expected completed resumed run, got %q", resumedRun.Status)
	}

	steps, err = executorService.ListSteps(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list steps after resume: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps after resume, got %d", len(steps))
	}
	if steps[0].Status != executor.StepStatusCompleted {
		t.Fatalf("expected approval step completed after resume, got %q", steps[0].Status)
	}
	if steps[1].NodeID != "audit" {
		t.Fatalf("expected audit node after resume, got %q", steps[1].NodeID)
	}
}

func TestExecuteSyncRoutesConditionBranches(t *testing.T) {
	t.Parallel()

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "route-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "route", Type: "condition", Config: map[string]any{"input_key": "severity", "operator": "equals", "equals": "high"}},
			{ID: "high", Type: "audit_log", Config: map[string]any{"message": "high-path"}},
			{ID: "low", Type: "audit_log", Config: map[string]any{"message": "low-path"}},
		},
		Edges: []workflow.Edge{
			{From: "route", To: "high", Condition: "true"},
			{From: "route", To: "low", Condition: "false"},
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

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"severity": "high"})
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}

	steps, err := executorService.ListSteps(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 executed steps, got %d", len(steps))
	}
	if steps[1].NodeID != "high" {
		t.Fatalf("expected high branch to execute, got %q", steps[1].NodeID)
	}
}

func TestExecuteSyncCallsHTTPToolNode(t *testing.T) {
	t.Parallel()

	httpClient := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", req.Method)
		}

		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload.Input != "INC-42" {
			t.Fatalf("expected INC-42 payload, got %q", payload.Input)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"received":"INC-42","status":"accepted"}`)),
		}, nil
	})

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "tool-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "notify", Type: "http_tool", Config: map[string]any{"url": "https://api.example.com/notify", "method": "POST", "input_key": "ticket"}},
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
	).WithHTTPClient(httpClient)

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", createdWorkflow.ID, map[string]any{"ticket": "INC-42"})
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
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}

	output, ok := steps[0].Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", steps[0].Output)
	}
	if output["status_code"] != 200 {
		t.Fatalf("expected status_code 200, got %#v", output["status_code"])
	}

	body, ok := output["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected response body map, got %T", output["body"])
	}
	if body["received"] != "INC-42" {
		t.Fatalf("expected received INC-42, got %#v", body["received"])
	}
}

func TestExecuteSyncRejectsUnsafeHTTPToolTargetAtRuntime(t *testing.T) {
	t.Parallel()

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	repository := workflow.NewMemoryRepository()
	unsafeWorkflow := workflow.Workflow{
		ID:               "wf-unsafe",
		TenantID:         "tenant-a",
		Name:             "unsafe-tool-flow",
		Version:          1,
		Status:           workflow.StatusDraft,
		ValidationStatus: workflow.ValidationStatusValid,
		Definition: workflow.Definition{
			Name:    "unsafe-tool-flow",
			Version: 1,
			Nodes: []workflow.Node{
				{ID: "notify", Type: "http_tool", Config: map[string]any{"url": "http://127.0.0.1:8080/admin", "method": "POST"}},
			},
		},
	}
	if err := repository.Create(context.Background(), unsafeWorkflow); err != nil {
		t.Fatalf("seed unsafe workflow: %v", err)
	}

	httpClientCalled := false
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflow.NewService(repository),
		documentService,
		executor.NewMockLLMProvider(),
	).WithHTTPClient(stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		httpClientCalled = true
		return nil, nil
	}))

	run, err := executorService.ExecuteSync(context.Background(), "tenant-a", unsafeWorkflow.ID, nil)
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	if run.Status != executor.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
	if !strings.Contains(run.Error, "not allowed") {
		t.Fatalf("expected unsafe target error, got %q", run.Error)
	}
	if httpClientCalled {
		t.Fatal("expected unsafe target to be rejected before issuing an HTTP request")
	}
}

func TestResumeApprovalRejectsRun(t *testing.T) {
	t.Parallel()

	documentService := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := workflowService.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "reject-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "review", Type: "approval"},
			{ID: "audit", Type: "audit_log", Config: map[string]any{"message": "should-not-run"}},
		},
		Edges: []workflow.Edge{
			{From: "review", To: "audit"},
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

	rejectedRun, err := executorService.ResumeApproval(context.Background(), "tenant-a", run.ID, false, "needs changes", nil)
	if err != nil {
		t.Fatalf("resume rejection: %v", err)
	}
	if rejectedRun.Status != executor.RunStatusFailed {
		t.Fatalf("expected failed run after rejection, got %q", rejectedRun.Status)
	}

	steps, err := executorService.ListSteps(context.Background(), "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected only approval step after rejection, got %d", len(steps))
	}
}
