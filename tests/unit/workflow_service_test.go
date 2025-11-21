package unit_test

import (
	"context"
	"errors"
	"testing"

	"workflow/internal/workflow"
)

func TestValidateDefinitionAcceptsSupportedWorkflow(t *testing.T) {
	t.Parallel()

	result := workflow.ValidateDefinition(workflow.Definition{
		Name:    "doc-summary",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "retrieve", Type: "retrieve_documents"},
			{ID: "summarize", Type: "llm"},
		},
		Edges: []workflow.Edge{
			{From: "retrieve", To: "summarize"},
		},
	})

	if !result.Valid {
		t.Fatalf("expected valid definition, got errors: %+v", result.Errors)
	}
}

func TestValidateDefinitionRejectsDuplicateNodesAndBadEdges(t *testing.T) {
	t.Parallel()

	result := workflow.ValidateDefinition(workflow.Definition{
		Name:    "",
		Version: 0,
		Nodes: []workflow.Node{
			{ID: "dup", Type: "llm"},
			{ID: "dup", Type: "unknown"},
			{ID: "cond", Type: "condition"},
		},
		Edges: []workflow.Edge{
			{From: "dup", To: "missing"},
			{From: "dup", To: "dup"},
		},
	})

	if result.Valid {
		t.Fatal("expected invalid definition")
	}
	if len(result.Errors) < 4 {
		t.Fatalf("expected multiple validation errors, got %d", len(result.Errors))
	}
}

func TestValidateDefinitionRejectsConditionWithoutTrueFalseBranches(t *testing.T) {
	t.Parallel()

	result := workflow.ValidateDefinition(workflow.Definition{
		Name:    "conditional-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "route", Type: "condition"},
			{ID: "next", Type: "audit_log"},
		},
		Edges: []workflow.Edge{
			{From: "route", To: "next", Condition: "maybe"},
		},
	})

	if result.Valid {
		t.Fatal("expected invalid condition workflow")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected validation errors")
	}
}

func TestValidateDefinitionRejectsHTTPToolWithoutURL(t *testing.T) {
	t.Parallel()

	result := workflow.ValidateDefinition(workflow.Definition{
		Name:    "tool-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "notify", Type: "http_tool", Config: map[string]any{"method": "POST"}},
		},
	})

	if result.Valid {
		t.Fatal("expected invalid http tool workflow")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected validation errors")
	}
}

func TestCreateRejectsInvalidWorkflow(t *testing.T) {
	t.Parallel()

	service := workflow.NewService(workflow.NewMemoryRepository())
	_, validation, err := service.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "",
		Version: 1,
		Nodes:   nil,
	})
	if !errors.Is(err, workflow.ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition, got %v", err)
	}
	if validation.Valid {
		t.Fatal("expected validation failure")
	}
}

func TestWorkflowGetDoesNotLeakAcrossTenants(t *testing.T) {
	t.Parallel()

	service := workflow.NewService(workflow.NewMemoryRepository())
	createdWorkflow, _, err := service.Create(context.Background(), "tenant-a", workflow.Definition{
		Name:    "tenant-a-flow",
		Version: 1,
		Nodes: []workflow.Node{
			{ID: "retrieve", Type: "retrieve_documents"},
		},
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	_, err = service.Get(context.Background(), "tenant-b", createdWorkflow.ID)
	if !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
