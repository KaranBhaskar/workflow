package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appapi "workflow/internal/api"
	"workflow/internal/platform/health"
)

type stubChecker struct {
	name string
	err  error
}

func (s stubChecker) Name() string {
	return s.name
}

func (s stubChecker) Check(context.Context) error {
	return s.err
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	healthService := health.NewService(
		"workflow-api",
		time.Second,
		stubChecker{name: "postgres"},
		stubChecker{name: "redis"},
	)
	handler := appapi.NewHandler(logger, healthService, nil, nil, nil, nil)

	t.Run("liveness returns ok", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		assertContentType(t, resp)

		var report health.Report
		if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
			t.Fatalf("decode live report: %v", err)
		}

		if report.Status != health.StatusLive {
			t.Fatalf("expected live status %q, got %q", health.StatusLive, report.Status)
		}
	})

	t.Run("readiness returns dependency checks", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		assertContentType(t, resp)

		var report health.Report
		if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
			t.Fatalf("decode ready report: %v", err)
		}

		if report.Status != health.StatusReady {
			t.Fatalf("expected ready status %q, got %q", health.StatusReady, report.Status)
		}

		if len(report.Checks) != 2 {
			t.Fatalf("expected 2 checks, got %d", len(report.Checks))
		}
	})
}

func TestReadinessEndpointReturns503ForDependencyFailure(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	healthService := health.NewService(
		"workflow-api",
		time.Second,
		stubChecker{name: "postgres", err: errors.New("dial tcp: connection refused")},
	)
	handler := appapi.NewHandler(logger, healthService, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	var report health.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode not-ready report: %v", err)
	}

	if report.Status != health.StatusNotReady {
		t.Fatalf("expected status %q, got %q", health.StatusNotReady, report.Status)
	}
}

func assertContentType(t *testing.T, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}
}
