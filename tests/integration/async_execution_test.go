package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
