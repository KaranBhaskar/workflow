package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"workflow/internal/tenant"
)

var (
	ErrMissingAPIKey  = errors.New("missing api key")
	ErrInvalidAPIKey  = errors.New("invalid api key")
	ErrInactiveAPIKey = errors.New("inactive api key")
	ErrInactiveTenant = errors.New("inactive tenant")
)

type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

type APIKey struct {
	ID        string
	TenantID  string
	Label     string
	Plaintext string
	Status    Status
}

type Credential struct {
	ID       string
	TenantID string
	Label    string
	KeyHash  string
	Status   Status
}

type Identity struct {
	TenantID     string
	TenantName   string
	TenantStatus tenant.Status
	APIKeyID     string
	APIKeyLabel  string
}

type Repository interface {
	LookupByHash(ctx context.Context, keyHash string) (Credential, error)
}

type MemoryRepository struct {
	mu      sync.RWMutex
	records map[string]Credential
}

type Service struct {
	repository Repository
	tenants    *tenant.Service
}

func NewMemoryRepository(keys []APIKey) *MemoryRepository {
	records := make(map[string]Credential, len(keys))
	for _, key := range keys {
		hash := HashAPIKey(key.Plaintext)
		records[hash] = Credential{
			ID:       key.ID,
			TenantID: key.TenantID,
			Label:    key.Label,
			KeyHash:  hash,
			Status:   key.Status,
		}
	}

	return &MemoryRepository{
		records: records,
	}
}

func NewService(repository Repository, tenants *tenant.Service) *Service {
	return &Service{
		repository: repository,
		tenants:    tenants,
	}
}

func (r *MemoryRepository) LookupByHash(_ context.Context, keyHash string) (Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	credential, ok := r.records[keyHash]
	if !ok {
		return Credential{}, ErrInvalidAPIKey
	}

	return credential, nil
}

func (s *Service) Authenticate(ctx context.Context, rawKey string) (Identity, error) {
	trimmedKey := strings.TrimSpace(rawKey)
	if trimmedKey == "" {
		return Identity{}, ErrMissingAPIKey
	}

	credential, err := s.repository.LookupByHash(ctx, HashAPIKey(trimmedKey))
	if err != nil {
		return Identity{}, err
	}

	if credential.Status != StatusActive {
		return Identity{}, ErrInactiveAPIKey
	}

	resolvedTenant, err := s.tenants.Get(ctx, credential.TenantID)
	if err != nil {
		return Identity{}, ErrInvalidAPIKey
	}

	if resolvedTenant.Status != tenant.StatusActive {
		return Identity{}, ErrInactiveTenant
	}

	return Identity{
		TenantID:     resolvedTenant.ID,
		TenantName:   resolvedTenant.Name,
		TenantStatus: resolvedTenant.Status,
		APIKeyID:     credential.ID,
		APIKeyLabel:  credential.Label,
	}, nil
}

func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
