package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound          = errors.New("workflow not found")
	ErrInvalidTenant     = errors.New("invalid tenant")
	ErrInvalidDefinition = errors.New("invalid workflow definition")
)

type Status string

const (
	StatusDraft Status = "draft"
)

type ValidationStatus string

const (
	ValidationStatusValid   ValidationStatus = "valid"
	ValidationStatusInvalid ValidationStatus = "invalid"
)

type Workflow struct {
	ID               string           `json:"id"`
	TenantID         string           `json:"tenant_id"`
	Name             string           `json:"name"`
	Description      string           `json:"description,omitempty"`
	Version          int              `json:"version"`
	Status           Status           `json:"status"`
	ValidationStatus ValidationStatus `json:"validation_status"`
	Definition       Definition       `json:"definition"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type Definition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     int            `json:"version"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Nodes       []Node         `json:"nodes"`
	Edges       []Edge         `json:"edges"`
}

type Node struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config,omitempty"`
}

type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

type Repository interface {
	Create(ctx context.Context, workflow Workflow) error
	GetByID(ctx context.Context, tenantID, workflowID string) (Workflow, error)
}

type MemoryRepository struct {
	mu        sync.RWMutex
	workflows map[string]Workflow
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workflows: make(map[string]Workflow),
	}
}

func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
		newID:      newWorkflowID,
	}
}

func (r *MemoryRepository) Create(_ context.Context, workflow Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.workflows[workflow.ID] = workflow
	return nil
}

func (r *MemoryRepository) GetByID(_ context.Context, tenantID, workflowID string) (Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflow, ok := r.workflows[workflowID]
	if !ok || workflow.TenantID != tenantID {
		return Workflow{}, ErrNotFound
	}

	return workflow, nil
}

func (s *Service) Create(ctx context.Context, tenantID string, definition Definition) (Workflow, ValidationResult, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Workflow{}, ValidationResult{}, ErrInvalidTenant
	}

	validation := ValidateDefinition(definition)
	if !validation.Valid {
		return Workflow{}, validation, ErrInvalidDefinition
	}

	workflowID, err := s.newID()
	if err != nil {
		return Workflow{}, ValidationResult{}, fmt.Errorf("generate workflow id: %w", err)
	}

	now := s.now()
	workflow := Workflow{
		ID:               workflowID,
		TenantID:         tenantID,
		Name:             definition.Name,
		Description:      definition.Description,
		Version:          definition.Version,
		Status:           StatusDraft,
		ValidationStatus: ValidationStatusValid,
		Definition:       definition,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.repository.Create(ctx, workflow); err != nil {
		return Workflow{}, ValidationResult{}, fmt.Errorf("persist workflow: %w", err)
	}

	return workflow, validation, nil
}

func (s *Service) Get(ctx context.Context, tenantID, workflowID string) (Workflow, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Workflow{}, ErrInvalidTenant
	}

	return s.repository.GetByID(ctx, tenantID, workflowID)
}

func (s *Service) Validate(ctx context.Context, tenantID, workflowID string) (ValidationResult, error) {
	workflow, err := s.Get(ctx, tenantID, workflowID)
	if err != nil {
		return ValidationResult{}, err
	}

	return ValidateDefinition(workflow.Definition), nil
}

func ValidateDefinition(definition Definition) ValidationResult {
	errorsList := make([]ValidationError, 0)

	if strings.TrimSpace(definition.Name) == "" {
		errorsList = append(errorsList, ValidationError{Field: "name", Message: "name is required"})
	}
	if definition.Version <= 0 {
		errorsList = append(errorsList, ValidationError{Field: "version", Message: "version must be greater than zero"})
	}
	if len(definition.Nodes) == 0 {
		errorsList = append(errorsList, ValidationError{Field: "nodes", Message: "at least one node is required"})
	}

	nodeIDs := make(map[string]Node, len(definition.Nodes))
	incoming := make(map[string]int, len(definition.Nodes))
	outgoing := make(map[string]int, len(definition.Nodes))
	for index, node := range definition.Nodes {
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("nodes[%d].id", index), Message: "node id is required"})
			continue
		}
		if _, exists := nodeIDs[nodeID]; exists {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("nodes[%d].id", index), Message: "node id must be unique"})
			continue
		}
		if !supportedNodeType(strings.TrimSpace(node.Type)) {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("nodes[%d].type", index), Message: "unsupported node type"})
		}

		nodeIDs[nodeID] = node
	}

	for index, edge := range definition.Edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if from == "" || to == "" {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("edges[%d]", index), Message: "edge endpoints are required"})
			continue
		}
		if from == to {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("edges[%d]", index), Message: "self-referential edges are not allowed"})
		}
		if _, ok := nodeIDs[from]; !ok {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("edges[%d].from", index), Message: "edge source must reference an existing node"})
		}
		if _, ok := nodeIDs[to]; !ok {
			errorsList = append(errorsList, ValidationError{Field: fmt.Sprintf("edges[%d].to", index), Message: "edge destination must reference an existing node"})
		}

		outgoing[from]++
		incoming[to]++
	}

	rootCount := 0
	for nodeID, node := range nodeIDs {
		if incoming[nodeID] == 0 {
			rootCount++
		}
		if node.Type == "condition" && outgoing[nodeID] < 2 {
			errorsList = append(errorsList, ValidationError{Field: "edges", Message: "condition nodes must have at least two outgoing edges"})
		}
	}
	if len(nodeIDs) > 0 && rootCount == 0 {
		errorsList = append(errorsList, ValidationError{Field: "nodes", Message: "workflow must contain at least one root node"})
	}

	return ValidationResult{
		Valid:  len(errorsList) == 0,
		Errors: errorsList,
	}
}

func supportedNodeType(nodeType string) bool {
	switch nodeType {
	case "retrieve_documents", "llm", "condition", "http_tool", "approval", "audit_log":
		return true
	default:
		return false
	}
}

func newWorkflowID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}
