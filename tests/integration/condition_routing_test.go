package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConditionWorkflowRoutesSelectedBranch(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"route-flow",
		"version":1,
		"nodes":[
			{"id":"route","type":"condition","config":{"input_key":"severity","operator":"equals","equals":"high"}},
			{"id":"high","type":"audit_log","config":{"message":"high-path"}},
			{"id":"low","type":"audit_log","config":{"message":"low-path"}}
		],
		"edges":[
			{"from":"route","to":"high","condition":"true"},
			{"from":"route","to":"low","condition":"false"}
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

	execReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"severity":"high"}
	}`))
	execReq.Header.Set("X-API-Key", exampleTenantAKey)
	execRecorder := httptest.NewRecorder()
	handler.ServeHTTP(execRecorder, execReq)
	execResp := execRecorder.Result()
	defer execResp.Body.Close()

	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", execResp.StatusCode)
	}

	var runPayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&runPayload); err != nil {
		t.Fatalf("decode run response: %v", err)
	}

	stepsReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+runPayload.Run.ID+"/steps", nil)
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
		} `json:"steps"`
	}
	if err := json.NewDecoder(stepsResp.Body).Decode(&stepsPayload); err != nil {
		t.Fatalf("decode steps response: %v", err)
	}

	if len(stepsPayload.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(stepsPayload.Steps))
	}
	if stepsPayload.Steps[0].NodeID != "route" {
		t.Fatalf("expected route step first, got %q", stepsPayload.Steps[0].NodeID)
	}
	if stepsPayload.Steps[1].NodeID != "high" {
		t.Fatalf("expected high branch second, got %q", stepsPayload.Steps[1].NodeID)
	}
}
