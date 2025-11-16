package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"workflow/internal/auth"
	"workflow/internal/documents"
	"workflow/internal/platform/health"
)

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(statusCode int) {
	sr.statusCode = statusCode
	sr.ResponseWriter.WriteHeader(statusCode)
}

type server struct {
	healthService   *health.Service
	documentService *documents.Service
}

type documentEnvelope struct {
	Document documents.Document `json:"document"`
}

type chunksEnvelope struct {
	Chunks []documents.Chunk `json:"chunks"`
}

func NewHandler(logger *slog.Logger, healthService *health.Service, authenticator *auth.Service, documentService *documents.Service) http.Handler {
	srv := server{
		healthService:   healthService,
		documentService: documentService,
	}
	mux := http.NewServeMux()

	mux.Handle("GET /health/live", http.HandlerFunc(srv.handleLive))
	mux.Handle("GET /health/ready", http.HandlerFunc(srv.handleReady))
	mux.Handle("GET /v1/tenants/me", requireAPIKey(authenticator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := currentIdentity(w, r)
		if !ok {
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant": map[string]string{
				"id":     identity.TenantID,
				"name":   identity.TenantName,
				"status": string(identity.TenantStatus),
			},
			"api_key": map[string]string{
				"id":    identity.APIKeyID,
				"label": identity.APIKeyLabel,
			},
		})
	})))
	mux.Handle("POST /v1/documents", requireAPIKey(authenticator, http.HandlerFunc(srv.handleUploadDocument)))
	mux.Handle("GET /v1/documents/{documentID}", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetDocument)))
	mux.Handle("GET /v1/documents/{documentID}/chunks", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetDocumentChunks)))

	return requestLogger(logger, mux)
}

func (s server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.healthService.LiveReport())
}

func (s server) handleReady(w http.ResponseWriter, r *http.Request) {
	report, ready := s.healthService.ReadyReport(r.Context())
	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(w, statusCode, report)
}

func (s server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := s.documentContext(w, r)
	if !ok {
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing multipart file field")
		return
	}
	defer file.Close()

	contentReader, contentType, err := sniffedContent(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file")
		return
	}

	document, err := documentService.Create(r.Context(), identity.TenantID, documents.CreateParams{
		Filename:    header.Filename,
		ContentType: contentType,
		Content:     contentReader,
	})
	if err != nil {
		writeDocumentError(w, err, "failed to upload document")
		return
	}

	writeJSON(w, http.StatusCreated, documentEnvelope{Document: document})
}

func (s server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := s.documentContext(w, r)
	if !ok {
		return
	}

	document, err := documentService.Get(r.Context(), identity.TenantID, r.PathValue("documentID"))
	if err != nil {
		writeDocumentError(w, err, "failed to fetch document")
		return
	}

	writeJSON(w, http.StatusOK, documentEnvelope{Document: document})
}

func (s server) handleGetDocumentChunks(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := s.documentContext(w, r)
	if !ok {
		return
	}

	chunks, err := documentService.ListChunks(r.Context(), identity.TenantID, r.PathValue("documentID"))
	if err != nil {
		writeDocumentError(w, err, "failed to fetch document chunks")
		return
	}

	writeJSON(w, http.StatusOK, chunksEnvelope{Chunks: chunks})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(recorder, r)

		logger.Info(
			"http request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status_code", recorder.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{
		"error": message,
	})
}

func currentIdentity(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "request identity missing from context")
		return auth.Identity{}, false
	}

	return identity, true
}

func (s server) documentContext(w http.ResponseWriter, r *http.Request) (auth.Identity, *documents.Service, bool) {
	if s.documentService == nil {
		writeError(w, http.StatusServiceUnavailable, "document service is not configured")
		return auth.Identity{}, nil, false
	}

	identity, ok := currentIdentity(w, r)
	if !ok {
		return auth.Identity{}, nil, false
	}

	return identity, s.documentService, true
}

func writeDocumentError(w http.ResponseWriter, err error, fallback string) {
	statusCode := http.StatusInternalServerError
	message := fallback

	switch {
	case errors.Is(err, documents.ErrInvalidFilename), errors.Is(err, documents.ErrMissingContent), errors.Is(err, documents.ErrUnsupportedContentType):
		statusCode = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, documents.ErrNotFound):
		statusCode = http.StatusNotFound
		message = "document not found"
	}

	writeError(w, statusCode, message)
}

func sniffedContent(file io.Reader) (io.Reader, string, error) {
	head := make([]byte, 512)
	readBytes, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "", err
	}

	contentType := http.DetectContentType(head[:readBytes])
	return io.MultiReader(bytes.NewReader(head[:readBytes]), file), contentType, nil
}

func requireAPIKey(authenticator *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticator == nil {
			writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
			return
		}

		apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
		identity, err := authenticator.Authenticate(r.Context(), apiKey)
		if err != nil {
			statusCode := http.StatusUnauthorized
			message := "invalid api key"

			switch {
			case errors.Is(err, auth.ErrMissingAPIKey):
				message = "missing api key"
			case errors.Is(err, auth.ErrInactiveAPIKey), errors.Is(err, auth.ErrInactiveTenant):
				message = "inactive credential"
			}

			writeError(w, statusCode, message)
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), identity)))
	})
}
