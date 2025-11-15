package integration_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDocumentUploadCreatesTenantScopedMetadata(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	body, contentType := multipartBody(t, "file", "notes.txt", []byte("hello from tenant a"))
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", "dev-key-tenant-a")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var payload struct {
		Document struct {
			ID          string `json:"id"`
			TenantID    string `json:"tenant_id"`
			Filename    string `json:"filename"`
			SizeBytes   int64  `json:"size_bytes"`
			ObjectKey   string `json:"object_key"`
			Status      string `json:"status"`
			Checksum    string `json:"checksum"`
			ContentType string `json:"content_type"`
		} `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	if payload.Document.ID == "" {
		t.Fatal("expected document id to be set")
	}
	if payload.Document.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", payload.Document.TenantID)
	}
	if payload.Document.Filename != "notes.txt" {
		t.Fatalf("expected notes.txt, got %q", payload.Document.Filename)
	}
	if payload.Document.SizeBytes == 0 {
		t.Fatal("expected size_bytes to be populated")
	}
	if payload.Document.ObjectKey == "" {
		t.Fatal("expected object key to be populated")
	}
	if payload.Document.Status != "uploaded" {
		t.Fatalf("expected uploaded status, got %q", payload.Document.Status)
	}
	if payload.Document.Checksum == "" {
		t.Fatal("expected checksum to be populated")
	}
	if payload.Document.ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %q", payload.Document.ContentType)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID, nil)
	getReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when fetching uploaded document, got %d", getResp.StatusCode)
	}
}

func TestDocumentUploadIsTenantScoped(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	body, contentType := multipartBody(t, "file", "tenant-a.txt", []byte("tenant a secret"))
	createReq := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	createReq.Header.Set("Content-Type", contentType)
	createReq.Header.Set("X-API-Key", "dev-key-tenant-a")
	createRecorder := httptest.NewRecorder()

	handler.ServeHTTP(createRecorder, createReq)
	createResp := createRecorder.Result()
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}

	var payload struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID, nil)
	getReq.Header.Set("X-API-Key", "dev-key-tenant-b")
	getRecorder := httptest.NewRecorder()

	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant fetch, got %d", getResp.StatusCode)
	}
}

func TestDocumentUploadRequiresMultipartFile(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewBufferString("not-multipart"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-API-Key", "dev-key-tenant-a")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func multipartBody(t *testing.T, fieldName, filename string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create multipart file part: %v", err)
	}

	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart payload: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	return body, writer.FormDataContentType()
}
