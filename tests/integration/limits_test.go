package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkflowExecuteReplaysIdempotencyKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "idempotent.txt", []byte("workflow idempotency backend test"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	firstReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"backend"}
	}`))
	firstReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	firstReq.Header.Set("Idempotency-Key", "idem-sync-1")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, firstReq)
	firstResp := firstRecorder.Result()
	defer firstResp.Body.Close()

	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstResp.StatusCode)
	}

	var firstPayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(firstResp.Body).Decode(&firstPayload); err != nil {
		t.Fatalf("decode first run: %v", err)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"backend"}
	}`))
	secondReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	secondReq.Header.Set("Idempotency-Key", "idem-sync-1")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, secondReq)
	secondResp := secondRecorder.Result()
	defer secondResp.Body.Close()

	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", secondResp.StatusCode)
	}
	if secondResp.Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatal("expected replay header to be set")
	}

	var secondPayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(secondResp.Body).Decode(&secondPayload); err != nil {
		t.Fatalf("decode second run: %v", err)
	}
	if secondPayload.Run.ID != firstPayload.Run.ID {
		t.Fatalf("expected same run id on replay, got %q and %q", firstPayload.Run.ID, secondPayload.Run.ID)
	}
}

func TestWorkflowExecuteRejectsIdempotencyMismatch(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "idempotent-mismatch.txt", []byte("workflow idempotency mismatch"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	firstReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"first"}
	}`))
	firstReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	firstReq.Header.Set("Idempotency-Key", "idem-sync-2")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, firstReq)
	firstResp := firstRecorder.Result()
	defer firstResp.Body.Close()

	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstResp.StatusCode)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", bytes.NewBufferString(`{
		"mode":"sync",
		"input":{"query":"second"}
	}`))
	secondReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	secondReq.Header.Set("Idempotency-Key", "idem-sync-2")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, secondReq)
	secondResp := secondRecorder.Result()
	defer secondResp.Body.Close()

	if secondResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", secondResp.StatusCode)
	}
}

func TestWorkflowExecuteRateLimitsPerTenant(t *testing.T) {
	t.Parallel()

	app := newTestAppWithOptions(t, testAppOptions{
		TriggerLimit:  1,
		TriggerWindow: time.Minute,
	})
	tenantAWorkflow := createSingleNodeWorkflow(t, app.Handler, "dev-key-tenant-a", "tenant-a-flow")
	tenantBWorkflow := createSingleNodeWorkflow(t, app.Handler, "dev-key-tenant-b", "tenant-b-flow")

	firstReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+tenantAWorkflow+"/execute", bytes.NewBufferString(`{"mode":"sync"}`))
	firstReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	firstRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(firstRecorder, firstReq)
	firstResp := firstRecorder.Result()
	defer firstResp.Body.Close()

	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstResp.StatusCode)
	}
	if firstResp.Header.Get("X-RateLimit-Limit") != "1" {
		t.Fatalf("expected limit header 1, got %q", firstResp.Header.Get("X-RateLimit-Limit"))
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+tenantAWorkflow+"/execute", bytes.NewBufferString(`{"mode":"sync"}`))
	secondReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	secondRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(secondRecorder, secondReq)
	secondResp := secondRecorder.Result()
	defer secondResp.Body.Close()

	if secondResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", secondResp.StatusCode)
	}

	tenantBReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+tenantBWorkflow+"/execute", bytes.NewBufferString(`{"mode":"sync"}`))
	tenantBReq.Header.Set("X-API-Key", "dev-key-tenant-b")
	tenantBRecorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(tenantBRecorder, tenantBReq)
	tenantBResp := tenantBRecorder.Result()
	defer tenantBResp.Body.Close()

	if tenantBResp.StatusCode != http.StatusOK {
		t.Fatalf("expected tenant b to remain unaffected, got %d", tenantBResp.StatusCode)
	}
}

func createSingleNodeWorkflow(t *testing.T, handler http.Handler, apiKey, name string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(fmt.Sprintf(`{
		"name":"%s",
		"version":1,
		"nodes":[
			{"id":"audit","type":"audit_log","config":{"message":"done"}}
		]
	}`, name)))
	req.Header.Set("X-API-Key", apiKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var payload struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode workflow response: %v", err)
	}

	return payload.Workflow.ID
}
