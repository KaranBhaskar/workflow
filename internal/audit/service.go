package audit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"workflow/internal/platform/ids"
)

var ErrInvalidTenant = errors.New("invalid tenant")

type Event struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	RunID      string         `json:"run_id"`
	WorkflowID string         `json:"workflow_id"`
	StepID     string         `json:"step_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Type       string         `json:"type"`
	Message    string         `json:"message,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type RecordParams struct {
	RunID      string
	WorkflowID string
	StepID     string
	NodeID     string
	Type       string
	Message    string
	Metadata   map[string]any
}

type Repository interface {
	Create(ctx context.Context, event Event) error
	ListByRun(ctx context.Context, tenantID, runID string) ([]Event, error)
}

type MemoryRepository struct {
	mu     sync.RWMutex
	events map[string][]Event
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events: make(map[string][]Event),
	}
}

func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
		newID:      ids.New,
	}
}

func (s *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		s.now = now
	}

	return s
}

func (r *MemoryRepository) Create(_ context.Context, event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events[event.RunID] = append(r.events[event.RunID], event)
	return nil
}

func (r *MemoryRepository) ListByRun(_ context.Context, tenantID, runID string) ([]Event, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events := r.events[runID]
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if event.TenantID == tenantID {
			filtered = append(filtered, event)
		}
	}

	slices.SortFunc(filtered, func(left, right Event) int {
		switch {
		case left.CreatedAt.Before(right.CreatedAt):
			return -1
		case left.CreatedAt.After(right.CreatedAt):
			return 1
		default:
			return strings.Compare(left.ID, right.ID)
		}
	})

	return filtered, nil
}

func (s *Service) Record(ctx context.Context, tenantID string, params RecordParams) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrInvalidTenant
	}

	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate audit event id: %w", err)
	}

	event := Event{
		ID:         eventID,
		TenantID:   tenantID,
		RunID:      params.RunID,
		WorkflowID: params.WorkflowID,
		StepID:     params.StepID,
		NodeID:     params.NodeID,
		Type:       strings.TrimSpace(params.Type),
		Message:    strings.TrimSpace(params.Message),
		Metadata:   cloneMap(params.Metadata),
		CreatedAt:  s.now(),
	}

	return s.repository.Create(ctx, event)
}

func (s *Service) ListRunEvents(ctx context.Context, tenantID, runID string) ([]Event, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidTenant
	}

	return s.repository.ListByRun(ctx, tenantID, runID)
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}
