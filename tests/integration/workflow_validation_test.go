package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateWorkflowAndValidate(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	requestBody := bytes.NewBufferString(`{
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
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", requestBody)
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
			ID               string `json:"id"`
			TenantID         string `json:"tenant_id"`
			Name             string `json:"name"`
			ValidationStatus string `json:"validation_status"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createPayload); err != nil {
		t.Fatalf("decode create workflow response: %v", err)
	}

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

	getReq := httptest.NewRequest(http.MethodGet, "/v1/workflows/"+createPayload.Workflow.ID, nil)
	getReq.Header.Set("X-API-Key", exampleTenantAKey)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}

	validateReq := httptest.NewRequest(http.MethodPost, "/v1/workflows/"+createPayload.Workflow.ID+"/validate", nil)
	validateReq.Header.Set("X-API-Key", exampleTenantAKey)
	validateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(validateRecorder, validateReq)
	validateResp := validateRecorder.Result()
	defer validateResp.Body.Close()

	if validateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", validateResp.StatusCode)
	}

	var validationPayload struct {
		Validation struct {
			Valid bool `json:"valid"`
		} `json:"validation"`
	}
	if err := json.NewDecoder(validateResp.Body).Decode(&validationPayload); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}

	if !validationPayload.Validation.Valid {
		t.Fatal("expected workflow validation to succeed")
	}
}

func TestCreateWorkflowRejectsInvalidDefinition(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"",
		"version":1,
		"nodes":[{"id":"duplicate","type":"llm"},{"id":"duplicate","type":"unknown"}],
		"edges":[{"from":"duplicate","to":"missing"}]
	}`))
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var payload struct {
		Validation struct {
			Valid  bool `json:"valid"`
			Errors []struct {
				Field string `json:"field"`
			} `json:"errors"`
		} `json:"validation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode validation errors: %v", err)
	}

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
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(`{
		"name":"tenant-a-flow",
		"version":1,
		"nodes":[{"id":"retrieve","type":"retrieve_documents"}],
		"edges":[]
	}`))
	req.Header.Set("X-API-Key", exampleTenantAKey)
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
		t.Fatalf("decode create workflow response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/workflows/"+payload.Workflow.ID, nil)
	getReq.Header.Set("X-API-Key", exampleTenantBKey)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant workflow fetch, got %d", getResp.StatusCode)
	}
}
