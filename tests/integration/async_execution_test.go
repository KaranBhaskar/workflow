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

func TestAsyncWorkflowExecution(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, nil)
	documentID := uploadDocumentForExecution(t, app.Handler, "async.txt", []byte("async execution backend orchestration queue worker"))
	workflowID := createExecutionWorkflow(t, app.Handler, documentID)

	executeReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"async",
		"input":{"query":"queue worker"}
	}`))
	executeReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	executeRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(executeRecorder, executeReq)
	executeResp := executeRecorder.Result()
	defer executeResp.Body.Close()

	if executeResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", executeResp.StatusCode)
	}

	var executePayload struct {
		Run struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.NewDecoder(executeResp.Body).Decode(&executePayload); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	if executePayload.Run.Status != "queued" {
		t.Fatalf("expected queued run, got %q", executePayload.Run.Status)
	}

	drainAsyncJob(t, app)

	runReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID, nil)
	runReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	runRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(runRecorder, runReq)
	runResp := runRecorder.Result()
	defer runResp.Body.Close()

	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", runResp.StatusCode)
	}

	var runPayload struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.NewDecoder(runResp.Body).Decode(&runPayload); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runPayload.Run.Status != "completed" {
		t.Fatalf("expected completed async run, got %q", runPayload.Run.Status)
	}

	stepsReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID+"/steps", nil)
	stepsReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	stepsRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(stepsRecorder, stepsReq)
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
	if len(stepsPayload.Steps) != 3 {
		t.Fatalf("expected 3 async steps, got %d", len(stepsPayload.Steps))
	}
}

func TestAsyncWorkflowRetriesAndDeadLettersFailures(t *testing.T) {
	t.Parallel()

	failingClient := stubHTTPClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"upstream failed"}`)),
		}, nil
	})
	app := newTestApp(t, failingClient)

	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"failing-async-flow",
		"version":1,
		"nodes":[
			{"id":"notify","type":"http_tool","config":{"url":"https://tools.local/fail","method":"POST","input_key":"message"}}
		]
	}`))
	req.Header.Set("X-API-Key", "dev-key-tenant-a")
	recorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(recorder, req)
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

	executeReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/execute", bytes.NewBufferString(`{
		"mode":"async",
		"input":{"message":"retry me"}
	}`))
	executeReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	executeRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(executeRecorder, executeReq)
	executeResp := executeRecorder.Result()
	defer executeResp.Body.Close()

	if executeResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", executeResp.StatusCode)
	}

	var executePayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(executeResp.Body).Decode(&executePayload); err != nil {
		t.Fatalf("decode async response: %v", err)
	}

	for range 3 {
		drainAsyncJob(t, app)
	}

	runReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID, nil)
	runReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	runRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(runRecorder, runReq)
	runResp := runRecorder.Result()
	defer runResp.Body.Close()

	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", runResp.StatusCode)
	}

	var runPayload struct {
		Run struct {
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
		} `json:"run"`
	}
	if err := json.NewDecoder(runResp.Body).Decode(&runPayload); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runPayload.Run.Status != "dead_letter" {
		t.Fatalf("expected dead_letter status, got %q", runPayload.Run.Status)
	}
	if runPayload.Run.Attempt != 3 {
		t.Fatalf("expected 3 attempts, got %d", runPayload.Run.Attempt)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs?status=dead_letter", nil)
	listReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	listRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(listRecorder, listReq)
	listResp := listRecorder.Result()
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", listResp.StatusCode)
	}

	var listPayload struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listPayload); err != nil {
		t.Fatalf("decode run list response: %v", err)
	}
	if len(listPayload.Runs) == 0 || listPayload.Runs[0].ID != executePayload.Run.ID {
		t.Fatal("expected dead-letter run to be listed")
	}
}
