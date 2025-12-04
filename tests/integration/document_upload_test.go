package integration_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentUploadCreatesTenantScopedMetadata(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	body, contentType := multipartBody(t, "file", "notes.txt", []byte("hello from tenant a"))
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", exampleTenantAKey)
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
			ChunkCount  int    `json:"chunk_count"`
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
	if payload.Document.Status != "ingested" {
		t.Fatalf("expected ingested status, got %q", payload.Document.Status)
	}
	if payload.Document.Checksum == "" {
		t.Fatal("expected checksum to be populated")
	}
	if payload.Document.ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %q", payload.Document.ContentType)
	}
	if payload.Document.ChunkCount == 0 {
		t.Fatal("expected chunk_count to be populated")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID, nil)
	getReq.Header.Set("X-API-Key", exampleTenantAKey)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when fetching uploaded document, got %d", getResp.StatusCode)
	}
}

func TestDocumentUploadCreatesChunks(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	body, contentType := multipartBody(t, "file", "long.txt", []byte(strings.Repeat("chunk words ", 120)))
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", exampleTenantAKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var payload struct {
		Document struct {
			ID         string `json:"id"`
			ChunkCount int    `json:"chunk_count"`
		} `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	chunksReq := httptest.NewRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID+"/chunks", nil)
	chunksReq.Header.Set("X-API-Key", exampleTenantAKey)
	chunksRecorder := httptest.NewRecorder()
	handler.ServeHTTP(chunksRecorder, chunksReq)
	chunksResp := chunksRecorder.Result()
	defer chunksResp.Body.Close()

	if chunksResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when fetching chunks, got %d", chunksResp.StatusCode)
	}

	var chunksPayload struct {
		Chunks []struct {
			ChunkIndex    int    `json:"chunk_index"`
			Content       string `json:"content"`
			TokenEstimate int    `json:"token_estimate"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(chunksResp.Body).Decode(&chunksPayload); err != nil {
		t.Fatalf("decode chunks response: %v", err)
	}

	if len(chunksPayload.Chunks) != payload.Document.ChunkCount {
		t.Fatalf("expected %d chunks, got %d", payload.Document.ChunkCount, len(chunksPayload.Chunks))
	}
	if len(chunksPayload.Chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunksPayload.Chunks))
	}
	if chunksPayload.Chunks[0].Content == "" {
		t.Fatal("expected first chunk to contain content")
	}
	if chunksPayload.Chunks[0].TokenEstimate == 0 {
		t.Fatal("expected token estimate to be populated")
	}
}

func TestDocumentUploadIsTenantScoped(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	body, contentType := multipartBody(t, "file", "tenant-a.txt", []byte("tenant a secret"))
	createReq := httptest.NewRequest(http.MethodPost, "/v1/documents", body)
	createReq.Header.Set("Content-Type", contentType)
	createReq.Header.Set("X-API-Key", exampleTenantAKey)
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
	getReq.Header.Set("X-API-Key", exampleTenantBKey)
	getRecorder := httptest.NewRecorder()

	handler.ServeHTTP(getRecorder, getReq)
	getResp := getRecorder.Result()
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant fetch, got %d", getResp.StatusCode)
	}

	chunksReq := httptest.NewRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID+"/chunks", nil)
	chunksReq.Header.Set("X-API-Key", exampleTenantBKey)
	chunksRecorder := httptest.NewRecorder()

	handler.ServeHTTP(chunksRecorder, chunksReq)
	chunksResp := chunksRecorder.Result()
	defer chunksResp.Body.Close()

	if chunksResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant chunk fetch, got %d", chunksResp.StatusCode)
	}
}

func TestDocumentUploadRequiresMultipartFile(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewBufferString("not-multipart"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-API-Key", exampleTenantAKey)
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
