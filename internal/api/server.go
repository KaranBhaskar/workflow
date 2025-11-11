package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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

func NewHandler(logger *slog.Logger, healthService *health.Service) http.Handler {
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
