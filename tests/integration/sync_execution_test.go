package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestSyncWorkflowExecution(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "design.txt", []byte("workflow architecture backend reliability orchestration platform"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	executeResp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{
		"mode":"sync",
		"input":{"query":"backend orchestration"},
		"options":{"timeout_ms":500}
	}`))
	defer executeResp.Body.Close()
	expectStatus(t, executeResp, http.StatusOK)

	var executePayload struct {
		Run struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			WorkflowID string `json:"workflow_id"`
			Output     struct {
				LastNode string `json:"last_node"`
			} `json:"output"`
		} `json:"run"`
	}
	decodeJSONResponse(t, executeResp, &executePayload)

	if executePayload.Run.ID == "" {
		t.Fatal("expected run id")
	}
	if executePayload.Run.Status != "completed" {
		t.Fatalf("expected completed run, got %q", executePayload.Run.Status)
	}
	if executePayload.Run.WorkflowID != workflowID {
		t.Fatalf("expected workflow id %q, got %q", workflowID, executePayload.Run.WorkflowID)
	}
	if executePayload.Run.Output.LastNode != "audit" {
		t.Fatalf("expected last node audit, got %q", executePayload.Run.Output.LastNode)
	}

	runResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID, exampleTenantAKey, nil))
	defer runResp.Body.Close()
	expectStatus(t, runResp, http.StatusOK)

	stepsResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID+"/steps", exampleTenantAKey, nil))
	defer stepsResp.Body.Close()
	expectStatus(t, stepsResp, http.StatusOK)

	var stepsPayload struct {
		Steps []struct {
			NodeID string `json:"node_id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	decodeJSONResponse(t, stepsResp, &stepsPayload)

	if len(stepsPayload.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(stepsPayload.Steps))
	}
	if stepsPayload.Steps[0].Status != "completed" {
		t.Fatalf("expected first step completed, got %q", stepsPayload.Steps[0].Status)
	}
}

func TestExecutionRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	workflowID := createExecutionWorkflow(t, handler, uploadDocumentForExecution(t, handler, "doc.txt", []byte("hello workflow execution")))

	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{"mode":"later"}`))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusBadRequest)
}

func uploadDocumentForExecution(t *testing.T, handler http.Handler, filename string, payload []byte) string {
	t.Helper()

	resp := performRequest(handler, newAuthedMultipartRequest(t, "/v1/documents", exampleTenantAKey, "file", filename, payload))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var envelope struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	decodeJSONResponse(t, resp, &envelope)

	return envelope.Document.ID
}

func createExecutionWorkflow(t *testing.T, handler http.Handler, documentID string) string {
	t.Helper()

	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", exampleTenantAKey, fmt.Sprintf(`{
		"name":"sync-flow",
		"version":1,
		"nodes":[
			{"id":"retrieve","type":"retrieve_documents","config":{"document_ids":["%s"],"query_input_key":"query","limit":2}},
			{"id":"summarize","type":"llm","config":{"prompt":"Summarize retrieved context","input_key":"query","context_step":"retrieve"}},
			{"id":"audit","type":"audit_log","config":{"message":"sync-run-complete","from_step":"summarize"}}
		],
		"edges":[
			{"from":"retrieve","to":"summarize"},
			{"from":"summarize","to":"audit"}
		]
	}`, documentID)))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var envelope struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	decodeJSONResponse(t, resp, &envelope)

	return envelope.Workflow.ID
}
