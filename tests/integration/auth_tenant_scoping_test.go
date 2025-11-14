package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appapi "workflow/internal/api"
	"workflow/internal/auth"
	"workflow/internal/platform/health"
	"workflow/internal/tenant"
)

func TestTenantEndpointRequiresAPIKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler()
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/me", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	assertContentType(t, resp)
}

func TestTenantEndpointRejectsInvalidAPIKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler()
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/me", nil)
	req.Header.Set("X-API-Key", "not-a-real-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestTenantEndpointScopesIdentityByAPIKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler()

	t.Run("tenant a receives its own identity", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants/me", nil)
		req.Header.Set("X-API-Key", "dev-key-tenant-a")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		var payload struct {
			Tenant struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tenant"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode tenant response: %v", err)
		}

		if payload.Tenant.ID != "tenant-a" {
			t.Fatalf("expected tenant-a, got %q", payload.Tenant.ID)
		}

		if payload.Tenant.Name != "Tenant A" {
			t.Fatalf("expected tenant name Tenant A, got %q", payload.Tenant.Name)
		}
	})

	t.Run("tenant b receives its own identity", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants/me", nil)
		req.Header.Set("X-API-Key", "dev-key-tenant-b")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		var payload struct {
			Tenant struct {
				ID string `json:"id"`
			} `json:"tenant"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode tenant response: %v", err)
		}

		if payload.Tenant.ID != "tenant-b" {
			t.Fatalf("expected tenant-b, got %q", payload.Tenant.ID)
		}
	})
}

func newAuthenticatedHandler() http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	healthService := health.NewService("workflow-api", time.Second)
	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusActive},
		{ID: "tenant-b", Name: "Tenant B", Status: tenant.StatusActive},
	}))
	authService := auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "key-a",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "dev-key-tenant-a",
			Status:    auth.StatusActive,
		},
		{
			ID:        "key-b",
			TenantID:  "tenant-b",
			Label:     "bootstrap",
			Plaintext: "dev-key-tenant-b",
			Status:    auth.StatusActive,
		},
	}), tenantService)

	return appapi.NewHandler(logger, healthService, authService)
}

func TestTenantEndpointMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler()
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/me", nil)
	req.Header.Set("X-API-Key", "dev-key-tenant-a")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestTenantEndpointPreservesHealthRoutesWithoutAuth(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler()
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuthServiceIgnoresUnusedContextCancellationForValidLookup(t *testing.T) {
	t.Parallel()

	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusActive},
	}))
	service := auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "key-a",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "dev-key-tenant-a",
			Status:    auth.StatusActive,
		},
	}), tenantService)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Authenticate(ctx, "dev-key-tenant-a"); err != nil {
		t.Fatalf("expected cached lookup to succeed despite canceled context, got %v", err)
	}
}
