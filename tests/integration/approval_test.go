package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApprovalWorkflowResume(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"approval-flow",
		"version":1,
		"nodes":[
			{"id":"review","type":"approval","config":{"message":"review required"}},
			{"id":"audit","type":"audit_log","config":{"message":"approved"}}
		],
		"edges":[
			{"from":"review","to":"audit"}
		]
	}`))
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var createPayload struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createPayload); err != nil {
		t.Fatalf("decode workflow response: %v", err)
	}

	execReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/execute", bytes.NewBufferString(`{"mode":"sync"}`))
	execReq.Header.Set("X-API-Key", exampleTenantAKey)
	execRecorder := httptest.NewRecorder()
	handler.ServeHTTP(execRecorder, execReq)
	execResp := execRecorder.Result()
	defer execResp.Body.Close()

	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", execResp.StatusCode)
	}

	var executePayload struct {
		Run struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&executePayload); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if executePayload.Run.Status != "waiting_approval" {
		t.Fatalf("expected waiting approval status, got %q", executePayload.Run.Status)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/v1/workflow-runs/"+executePayload.Run.ID+"/resume", bytes.NewBufferString(`{
		"approved":true,
		"comment":"ship it"
	}`))
	resumeReq.Header.Set("X-API-Key", exampleTenantAKey)
	resumeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(resumeRecorder, resumeReq)
	resumeResp := resumeRecorder.Result()
	defer resumeResp.Body.Close()

	if resumeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resumeResp.StatusCode)
	}

	var resumePayload struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.NewDecoder(resumeResp.Body).Decode(&resumePayload); err != nil {
		t.Fatalf("decode resume response: %v", err)
	}
	if resumePayload.Run.Status != "completed" {
		t.Fatalf("expected completed status after resume, got %q", resumePayload.Run.Status)
	}

	stepsReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID+"/steps", nil)
	stepsReq.Header.Set("X-API-Key", exampleTenantAKey)
	stepsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(stepsRecorder, stepsReq)
	stepsResp := stepsRecorder.Result()
	defer stepsResp.Body.Close()

	if stepsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", stepsResp.StatusCode)
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
	if len(stepsPayload.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(stepsPayload.Steps))
	}
	if stepsPayload.Steps[0].Status != "completed" {
		t.Fatalf("expected approval step completed, got %q", stepsPayload.Steps[0].Status)
	}
}
