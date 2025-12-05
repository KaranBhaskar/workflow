package integration_test

import (
	"net/http"
	"testing"
)

func TestApprovalWorkflowResume(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", exampleTenantAKey, `{
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
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var createPayload struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	decodeJSONResponse(t, resp, &createPayload)

	execResp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/execute", exampleTenantAKey, `{"mode":"sync"}`))
	defer execResp.Body.Close()
	expectStatus(t, execResp, http.StatusOK)

	var executePayload struct {
		Run struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	decodeJSONResponse(t, execResp, &executePayload)
	if executePayload.Run.Status != "waiting_approval" {
		t.Fatalf("expected waiting approval status, got %q", executePayload.Run.Status)
	}

	resumeResp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflow-runs/"+executePayload.Run.ID+"/resume", exampleTenantAKey, `{
		"approved":true,
		"comment":"ship it"
	}`))
	defer resumeResp.Body.Close()
	expectStatus(t, resumeResp, http.StatusOK)

	var resumePayload struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	decodeJSONResponse(t, resumeResp, &resumePayload)
	if resumePayload.Run.Status != "completed" {
		t.Fatalf("expected completed status after resume, got %q", resumePayload.Run.Status)
	}

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
	if len(stepsPayload.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(stepsPayload.Steps))
	}
	if stepsPayload.Steps[0].Status != "completed" {
		t.Fatalf("expected approval step completed, got %q", stepsPayload.Steps[0].Status)
	}
}
