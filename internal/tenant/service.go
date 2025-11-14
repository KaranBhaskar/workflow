package tenant

import (
	"context"
	"errors"
	"sync"
)

var ErrNotFound = errors.New("tenant not found")

type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

type Tenant struct {
	ID     string
	Name   string
	Status Status
}

type Repository interface {
	GetByID(ctx context.Context, id string) (Tenant, error)
}

type MemoryRepository struct {
	mu      sync.RWMutex
	records map[string]Tenant
}

type Service struct {
	repository Repository
}

func NewMemoryRepository(tenants []Tenant) *MemoryRepository {
	records := make(map[string]Tenant, len(tenants))
	for _, tenant := range tenants {
		records[tenant.ID] = tenant
	}

	return &MemoryRepository{
		records: records,
	}
}

func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
	}
}

func (r *MemoryRepository) GetByID(_ context.Context, id string) (Tenant, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tenant, ok := r.records[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}

	return tenant, nil
}

func (s *Service) Get(ctx context.Context, id string) (Tenant, error) {
	return s.repository.GetByID(ctx, id)
}
