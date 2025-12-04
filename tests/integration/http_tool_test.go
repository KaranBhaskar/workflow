package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPToolWorkflowExecution(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandlerWithHTTPClient(t, stubHTTPClient(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"received":"` + payload.Input + `","result":"queued"}`)),
		}, nil
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"tool-flow",
		"version":1,
		"nodes":[
			{"id":"notify","type":"http_tool","config":{"url":"https://api.example.com/notify","method":"POST","input_key":"ticket"}}
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
		"input":{"ticket":"INC-9000"}
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
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&runPayload); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if runPayload.Run.Status != "completed" {
		t.Fatalf("expected completed run, got %q", runPayload.Run.Status)
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
			NodeID string         `json:"node_id"`
			Output map[string]any `json:"output"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(stepsResp.Body).Decode(&stepsPayload); err != nil {
		t.Fatalf("decode steps response: %v", err)
	}

	if len(stepsPayload.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(stepsPayload.Steps))
	}
	if stepsPayload.Steps[0].NodeID != "notify" {
		t.Fatalf("expected notify step, got %q", stepsPayload.Steps[0].NodeID)
	}

	body, ok := stepsPayload.Steps[0].Output["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected response body map, got %T", stepsPayload.Steps[0].Output["body"])
	}
	if body["received"] != "INC-9000" {
		t.Fatalf("expected received INC-9000, got %#v", body["received"])
	}
}

func TestHTTPToolWorkflowRejectsUnsafeTarget(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"tool-flow",
		"version":1,
		"nodes":[
			{"id":"notify","type":"http_tool","config":{"url":"http://localhost:9000/admin","method":"POST"}}
		]
	}`))
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
