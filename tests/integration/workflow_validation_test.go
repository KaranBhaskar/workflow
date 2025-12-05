package integration_test

import (
	"net/http"
	"testing"
)

func TestCreateWorkflowAndValidate(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", exampleTenantAKey, `{
		"name":"doc-summary",
		"description":"Summarize a document",
		"version":1,
		"nodes":[
			{"id":"retrieve","type":"retrieve_documents"},
			{"id":"summarize","type":"llm"},
			{"id":"audit","type":"audit_log"}
		],
		"edges":[
			{"from":"retrieve","to":"summarize"},
			{"from":"summarize","to":"audit"}
		]
	}`))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var createPayload struct {
		Workflow struct {
			ID               string `json:"id"`
			TenantID         string `json:"tenant_id"`
			Name             string `json:"name"`
			ValidationStatus string `json:"validation_status"`
		} `json:"workflow"`
	}
	decodeJSONResponse(t, resp, &createPayload)

	if createPayload.Workflow.ID == "" {
		t.Fatal("expected workflow id to be set")
	}
	if createPayload.Workflow.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", createPayload.Workflow.TenantID)
	}
	if createPayload.Workflow.Name != "doc-summary" {
		t.Fatalf("expected doc-summary, got %q", createPayload.Workflow.Name)
	}
	if createPayload.Workflow.ValidationStatus != "valid" {
		t.Fatalf("expected valid status, got %q", createPayload.Workflow.ValidationStatus)
	}

	getResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/workflows/"+createPayload.Workflow.ID, exampleTenantAKey, nil))
	defer getResp.Body.Close()
	expectStatus(t, getResp, http.StatusOK)

	validateResp := performRequest(handler, newAuthedRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/validate", exampleTenantAKey, nil))
	defer validateResp.Body.Close()
	expectStatus(t, validateResp, http.StatusOK)

	var validationPayload struct {
		Validation struct {
			Valid bool `json:"valid"`
		} `json:"validation"`
	}
	decodeJSONResponse(t, validateResp, &validationPayload)

	if !validationPayload.Validation.Valid {
		t.Fatal("expected workflow validation to succeed")
	}
}

func TestCreateWorkflowRejectsInvalidDefinition(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", exampleTenantAKey, `{
		"name":"",
		"version":1,
		"nodes":[{"id":"duplicate","type":"llm"},{"id":"duplicate","type":"unknown"}],
		"edges":[{"from":"duplicate","to":"missing"}]
	}`))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusBadRequest)

	var payload struct {
		Validation struct {
			Valid  bool `json:"valid"`
			Errors []struct {
				Field string `json:"field"`
			} `json:"errors"`
		} `json:"validation"`
	}
	decodeJSONResponse(t, resp, &payload)

	if payload.Validation.Valid {
		t.Fatal("expected invalid workflow definition")
	}
	if len(payload.Validation.Errors) == 0 {
		t.Fatal("expected validation errors")
	}
}

func TestWorkflowLookupIsTenantScoped(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedJSONRequest(http.MethodPost, "/v1/workflows", exampleTenantAKey, `{
		"name":"tenant-a-flow",
		"version":1,
		"nodes":[{"id":"retrieve","type":"retrieve_documents"}],
		"edges":[]
	}`))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var payload struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	decodeJSONResponse(t, resp, &payload)

	getResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/workflows/"+payload.Workflow.ID, exampleTenantBKey, nil))
	defer getResp.Body.Close()
	expectStatus(t, getResp, http.StatusNotFound)
}
