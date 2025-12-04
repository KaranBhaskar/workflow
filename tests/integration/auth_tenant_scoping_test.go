package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantEndpointRequiresAPIKey(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
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

	handler := newAuthenticatedHandler(t)
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

	handler := newAuthenticatedHandler(t)

	t.Run("tenant a receives its own identity", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants/me", nil)
		req.Header.Set("X-API-Key", exampleTenantAKey)
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
		req.Header.Set("X-API-Key", exampleTenantBKey)
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

func TestTenantEndpointMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/me", nil)
	req.Header.Set("X-API-Key", exampleTenantAKey)
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

	handler := newAuthenticatedHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
