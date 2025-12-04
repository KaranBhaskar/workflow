package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkflowRunEventsExposeAuditTrail(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "audit.txt", []byte("audit trail backend workflow execution"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	executeReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"audit trail"}
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
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(executeResp.Body).Decode(&executePayload); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/v1/workflow-runs/"+executePayload.Run.ID+"/events", nil)
	eventsReq.Header.Set("X-API-Key", exampleTenantAKey)
	eventsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(eventsRecorder, eventsReq)
	eventsResp := eventsRecorder.Result()
	defer eventsResp.Body.Close()

	if eventsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", eventsResp.StatusCode)
	}

	var eventsPayload struct {
		Events []struct {
			Type   string `json:"type"`
			NodeID string `json:"node_id"`
		} `json:"events"`
	}
	if err := json.NewDecoder(eventsResp.Body).Decode(&eventsPayload); err != nil {
		t.Fatalf("decode events response: %v", err)
	}

	if len(eventsPayload.Events) < 5 {
		t.Fatalf("expected several audit events, got %d", len(eventsPayload.Events))
	}
	if eventsPayload.Events[0].Type != "run_started" {
		t.Fatalf("expected first event run_started, got %q", eventsPayload.Events[0].Type)
	}
	if eventsPayload.Events[len(eventsPayload.Events)-1].Type != "run_completed" {
		t.Fatalf("expected last event run_completed, got %q", eventsPayload.Events[len(eventsPayload.Events)-1].Type)
	}

	foundAuditStep := false
	for _, event := range eventsPayload.Events {
		if event.Type == "step_completed" && event.NodeID == "audit" {
			foundAuditStep = true
			break
		}
	}
	if !foundAuditStep {
		t.Fatal("expected audit node completion event")
	}
}
