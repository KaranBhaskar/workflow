package integration_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentUploadCreatesTenantScopedMetadata(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedMultipartRequest(t, "/v1/documents", exampleTenantAKey, "file", "notes.txt", []byte("hello from tenant a")))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

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
	decodeJSONResponse(t, resp, &payload)

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

	getResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID, exampleTenantAKey, nil))
	defer getResp.Body.Close()
	expectStatus(t, getResp, http.StatusOK)
}

func TestDocumentUploadCreatesChunks(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newAuthedMultipartRequest(t, "/v1/documents", exampleTenantAKey, "file", "long.txt", []byte(strings.Repeat("chunk words ", 120))))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusCreated)

	var payload struct {
		Document struct {
			ID         string `json:"id"`
			ChunkCount int    `json:"chunk_count"`
		} `json:"document"`
	}
	decodeJSONResponse(t, resp, &payload)

	chunksResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID+"/chunks", exampleTenantAKey, nil))
	defer chunksResp.Body.Close()
	expectStatus(t, chunksResp, http.StatusOK)

	var chunksPayload struct {
		Chunks []struct {
			ChunkIndex    int    `json:"chunk_index"`
			Content       string `json:"content"`
			TokenEstimate int    `json:"token_estimate"`
		} `json:"chunks"`
	}
	decodeJSONResponse(t, chunksResp, &chunksPayload)

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
	createResp := performRequest(handler, newAuthedMultipartRequest(t, "/v1/documents", exampleTenantAKey, "file", "tenant-a.txt", []byte("tenant a secret")))
	defer createResp.Body.Close()
	expectStatus(t, createResp, http.StatusCreated)

	var payload struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	decodeJSONResponse(t, createResp, &payload)

	getResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID, exampleTenantBKey, nil))
	defer getResp.Body.Close()
	expectStatus(t, getResp, http.StatusNotFound)

	chunksResp := performRequest(handler, newAuthedRequest(http.MethodGet, "/v1/documents/"+payload.Document.ID+"/chunks", exampleTenantBKey, nil))
	defer chunksResp.Body.Close()
	expectStatus(t, chunksResp, http.StatusNotFound)
}

func TestDocumentUploadRequiresMultipartFile(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	resp := performRequest(handler, newRequest(http.MethodPost, "/v1/documents", strings.NewReader("not-multipart"), map[string]string{
		"Content-Type": "text/plain",
		"X-API-Key":    exampleTenantAKey,
	}))
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusBadRequest)
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
