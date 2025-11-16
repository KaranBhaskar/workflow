package unit_test

import (
	"context"
	"errors"
	"testing"

	"workflow/internal/auth"
	"workflow/internal/tenant"
)

func TestAuthenticateRejectsMissingAPIKey(t *testing.T) {
	t.Parallel()

	service := newAuthServiceForTests()
	_, err := service.Authenticate(context.Background(), "")
	if !errors.Is(err, auth.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}
}

func TestAuthenticateRejectsUnknownAPIKey(t *testing.T) {
	t.Parallel()

	service := newAuthServiceForTests()
	_, err := service.Authenticate(context.Background(), "does-not-exist")
	if !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Fatalf("expected ErrInvalidAPIKey, got %v", err)
	}
}

func TestAuthenticateReturnsTenantScopedIdentity(t *testing.T) {
	t.Parallel()

	service := newAuthServiceForTests()
	identity, err := service.Authenticate(context.Background(), "tenant-a-key")
	if err != nil {
		t.Fatalf("authenticate valid key: %v", err)
	}

	if identity.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", identity.TenantID)
	}

	if identity.TenantName != "Tenant A" {
		t.Fatalf("expected Tenant A, got %q", identity.TenantName)
	}

	if identity.APIKeyLabel != "bootstrap" {
		t.Fatalf("expected bootstrap label, got %q", identity.APIKeyLabel)
	}
}

func TestAuthenticateRejectsInactiveAPIKey(t *testing.T) {
	t.Parallel()

	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusActive},
	}))
	service := auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "inactive-key",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "tenant-a-key",
			Status:    auth.StatusInactive,
		},
	}), tenantService)

	_, err := service.Authenticate(context.Background(), "tenant-a-key")
	if !errors.Is(err, auth.ErrInactiveAPIKey) {
		t.Fatalf("expected ErrInactiveAPIKey, got %v", err)
	}
}

func TestAuthenticateRejectsInactiveTenant(t *testing.T) {
	t.Parallel()

	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusInactive},
	}))
	service := auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "active-key",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "tenant-a-key",
			Status:    auth.StatusActive,
		},
	}), tenantService)

	_, err := service.Authenticate(context.Background(), "tenant-a-key")
	if !errors.Is(err, auth.ErrInactiveTenant) {
		t.Fatalf("expected ErrInactiveTenant, got %v", err)
	}
}

func TestAuthenticateAllowsCachedLookupWithCanceledContext(t *testing.T) {
	t.Parallel()

	service := newAuthServiceForTests()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Authenticate(ctx, "tenant-a-key"); err != nil {
		t.Fatalf("expected cached lookup to succeed despite canceled context, got %v", err)
	}
}

func newAuthServiceForTests() *auth.Service {
	tenantService := tenant.NewService(tenant.NewMemoryRepository([]tenant.Tenant{
		{ID: "tenant-a", Name: "Tenant A", Status: tenant.StatusActive},
		{ID: "tenant-b", Name: "Tenant B", Status: tenant.StatusActive},
	}))

	return auth.NewService(auth.NewMemoryRepository([]auth.APIKey{
		{
			ID:        "key-a",
			TenantID:  "tenant-a",
			Label:     "bootstrap",
			Plaintext: "tenant-a-key",
			Status:    auth.StatusActive,
		},
		{
			ID:        "key-b",
			TenantID:  "tenant-b",
			Label:     "bootstrap",
			Plaintext: "tenant-b-key",
			Status:    auth.StatusActive,
		},
	}), tenantService)
}
