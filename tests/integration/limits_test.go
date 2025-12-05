package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestWorkflowExecuteReplaysIdempotencyKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "idempotent.txt", []byte("workflow idempotency backend test"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	firstReq := newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{
		"mode":"sync",
		"input":{"query":"backend"}
	}`)
	firstReq.Header.Set("Idempotency-Key", "idem-sync-1")
	firstResp := performRequest(handler, firstReq)
	defer firstResp.Body.Close()
	expectStatus(t, firstResp, http.StatusOK)

	var firstPayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	decodeJSONResponse(t, firstResp, &firstPayload)

	secondReq := newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{
		"mode":"sync",
		"input":{"query":"backend"}
	}`)
	secondReq.Header.Set("Idempotency-Key", "idem-sync-1")
	secondResp := performRequest(handler, secondReq)
	defer secondResp.Body.Close()
	expectStatus(t, secondResp, http.StatusOK)
	if secondResp.Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatal("expected replay header to be set")
	}

	var secondPayload struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	decodeJSONResponse(t, secondResp, &secondPayload)
	if secondPayload.Run.ID != firstPayload.Run.ID {
		t.Fatalf("expected same run id on replay, got %q and %q", firstPayload.Run.ID, secondPayload.Run.ID)
	}
}

func TestWorkflowExecuteRejectsIdempotencyMismatch(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	documentID := uploadDocumentForExecution(t, handler, "idempotent-mismatch.txt", []byte("workflow idempotency mismatch"))
	workflowID := createExecutionWorkflow(t, handler, documentID)

	firstReq := newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{
		"mode":"sync",
		"input":{"query":"first"}
	}`)
	firstReq.Header.Set("Idempotency-Key", "idem-sync-2")
	firstResp := performRequest(handler, firstReq)
	defer firstResp.Body.Close()
	expectStatus(t, firstResp, http.StatusOK)

	secondReq := newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+workflowID+"/execute", exampleTenantAKey, `{
		"mode":"sync",
		"input":{"query":"second"}
	}`)
	secondReq.Header.Set("Idempotency-Key", "idem-sync-2")
	secondResp := performRequest(handler, secondReq)
	defer secondResp.Body.Close()
	expectStatus(t, secondResp, http.StatusConflict)
}

func TestWorkflowExecuteRateLimitsPerTenant(t *testing.T) {
	t.Parallel()

	app := newTestAppWithOptions(t, testAppOptions{
		TriggerLimit:  1,
		TriggerWindow: time.Minute,
	})
	tenantAWorkflow := createSingleNodeWorkflow(t, app.Handler, exampleTenantAKey, "tenant-a-flow")
	tenantBWorkflow := createSingleNodeWorkflow(t, app.Handler, exampleTenantBKey, "tenant-b-flow")

	firstResp := performRequest(app.Handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+tenantAWorkflow+"/execute", exampleTenantAKey, `{"mode":"sync"}`))
	defer firstResp.Body.Close()
	expectStatus(t, firstResp, http.StatusOK)
	if firstResp.Header.Get("X-RateLimit-Limit") != "1" {
		t.Fatalf("expected limit header 1, got %q", firstResp.Header.Get("X-RateLimit-Limit"))
	}

	secondResp := performRequest(app.Handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+tenantAWorkflow+"/execute", exampleTenantAKey, `{"mode":"sync"}`))
	defer secondResp.Body.Close()
	expectStatus(t, secondResp, http.StatusTooManyRequests)

	tenantBResp := performRequest(app.Handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+tenantBWorkflow+"/execute", exampleTenantBKey, `{"mode":"sync"}`))
	defer tenantBResp.Body.Close()
	expectStatus(t, tenantBResp, http.StatusOK)
}

func createSingleNodeWorkflow(t *testing.T, handler http.Handler, apiKey, name string) string {
	t.Helper()

	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", apiKey, fmt.Sprintf(`{
		"name":"%s",
		"version":1,
		"nodes":[
			{"id":"audit","type":"audit_log","config":{"message":"done"}}
		]
	}`, name)))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var payload struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	decodeJSONResponse(t, resp, &payload)

	return payload.Workflow.ID
}
