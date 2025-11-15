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

func NewHandler(logger *slog.Logger, healthService *health.Service, authenticator *auth.Service, documentService *documents.Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}

		writeJSON(w, http.StatusOK, healthService.LiveReport())
	})

	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}

		report, ready := healthService.ReadyReport(r.Context())
		statusCode := http.StatusOK
		if !ready {
			statusCode = http.StatusServiceUnavailable
		}

		writeJSON(w, statusCode, report)
	})

	mux.Handle("/v1/tenants/me", requireAPIKey(authenticator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}

		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "request identity missing from context",
			})
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

	mux.Handle("/v1/documents", requireAPIKey(authenticator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}

		if documentService == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "document service is not configured",
			})
			return
		}

		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "request identity missing from context",
			})
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "missing multipart file field",
			})
			return
		}
		defer file.Close()

		contentReader, contentType, err := snifffedContent(file)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "failed to read uploaded file",
			})
			return
		}

		document, err := documentService.Create(r.Context(), identity.TenantID, documents.CreateParams{
			Filename:    header.Filename,
			ContentType: contentType,
			Content:     contentReader,
		})
		if err != nil {
			statusCode := http.StatusInternalServerError
			message := "failed to upload document"

			switch {
			case errors.Is(err, documents.ErrInvalidFilename), errors.Is(err, documents.ErrMissingContent), errors.Is(err, documents.ErrUnsupportedContentType):
				statusCode = http.StatusBadRequest
				message = err.Error()
			}

			writeJSON(w, statusCode, map[string]string{
				"error": message,
			})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"document": documentResponse(document),
		})
	})))

	mux.Handle("/v1/documents/", requireAPIKey(authenticator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}

		if documentService == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "document service is not configured",
			})
			return
		}

		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "request identity missing from context",
			})
			return
		}

		documentID := strings.TrimPrefix(r.URL.Path, "/v1/documents/")
		if strings.TrimSpace(documentID) == "" || strings.Contains(documentID, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "document not found",
			})
			return
		}

		document, err := documentService.Get(r.Context(), identity.TenantID, documentID)
		if err != nil {
			statusCode := http.StatusInternalServerError
			message := "failed to fetch document"

			if errors.Is(err, documents.ErrNotFound) {
				statusCode = http.StatusNotFound
				message = "document not found"
			}

			writeJSON(w, statusCode, map[string]string{
				"error": message,
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"document": documentResponse(document),
		})
	})))

	return requestLogger(logger, mux)
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

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
		"error": "method not allowed",
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func documentResponse(document documents.Document) map[string]any {
	return map[string]any{
		"id":           document.ID,
		"tenant_id":    document.TenantID,
		"filename":     document.Filename,
		"content_type": document.ContentType,
		"size_bytes":   document.SizeBytes,
		"checksum":     document.Checksum,
		"object_key":   document.ObjectKey,
		"status":       string(document.Status),
		"created_at":   document.CreatedAt,
	}
}

func snifffedContent(file io.Reader) (io.Reader, string, error) {
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
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "authentication is not configured",
			})
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

			writeJSON(w, statusCode, map[string]string{
				"error": message,
			})
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), identity)))
	})
}
