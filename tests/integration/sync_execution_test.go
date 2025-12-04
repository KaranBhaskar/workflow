package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncWorkflowExecution(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "design.txt", []byte("workflow architecture backend reliability orchestration platform"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	executeReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"backend orchestration"},
		"options":{"timeout_ms":500}
	}`))
	executeReq.Header.Set("X-API-Key", exampleTenantAKey)
	executeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(executeRecorder, executeReq)
	executeResp := executeRecorder.Result()
	defer executeResp.Body.Close()

	if executeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", executeResp.StatusCode)
	}

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
	if err := json.NewDecoder(executeResp.Body).Decode(&executePayload); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}

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

	runReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID, nil)
	runReq.Header.Set("X-API-Key", exampleTenantAKey)
	runRecorder := httptest.NewRecorder()
	handler.ServeHTTP(runRecorder, runReq)
	runResp := runRecorder.Result()
	defer runResp.Body.Close()

	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when fetching run, got %d", runResp.StatusCode)
	}

	stepsReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID+"/steps", nil)
	stepsReq.Header.Set("X-API-Key", exampleTenantAKey)
	stepsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(stepsRecorder, stepsReq)
	stepsResp := stepsRecorder.Result()
	defer stepsResp.Body.Close()

	if stepsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when fetching steps, got %d", stepsResp.StatusCode)
	}

	var stepsPayload struct {
		Steps []struct {
			NodeID string `json:"node_id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(stepsResp.Body).Decode(&stepsPayload); err != nil {
		t.Fatalf("decode steps response: %v", err)
	}

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

	req := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{"mode":"later"}`))
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func uploadDocumentForExecution(t *testing.T, handler http.Handler, filename string, payload []byte) string {
	t.Helper()

	body, contentType := multipartBody(t, "file", filename, payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var envelope struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode document upload response: %v", err)
	}

	return envelope.Document.ID
}

func createExecutionWorkflow(t *testing.T, handler http.Handler, documentID string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(fmt.Sprintf(`{
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
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var envelope struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode workflow response: %v", err)
	}

	return envelope.Workflow.ID
}
