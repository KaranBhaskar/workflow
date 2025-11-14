package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"workflow/internal/auth"
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

func NewHandler(logger *slog.Logger, healthService *health.Service, authenticator *auth.Service) http.Handler {
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
