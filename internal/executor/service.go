package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"workflow/internal/audit"
	"workflow/internal/documents"
	"workflow/internal/platform/ids"
	"workflow/internal/tenant"
	"workflow/internal/workflow"
)

var (
	ErrNotFound             = errors.New("workflow run not found")
	ErrInvalidTenant        = errors.New("invalid tenant")
	ErrRunNotAwaitingReview = errors.New("workflow run is not awaiting approval")
	ErrAsyncDisabled        = errors.New("async execution is not configured")
)

type RunStatus string

const (
	RunStatusQueued          RunStatus = "queued"
	RunStatusRunning         RunStatus = "running"
	RunStatusWaitingApproval RunStatus = "waiting_approval"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusDeadLetter      RunStatus = "dead_letter"
)

type StepStatus string

const (
	StepStatusWaitingApproval StepStatus = "waiting_approval"
	StepStatusCompleted       StepStatus = "completed"
	StepStatusFailed          StepStatus = "failed"
)

type Run struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	WorkflowID  string         `json:"workflow_id"`
	Status      RunStatus      `json:"status"`
	TriggerMode string         `json:"trigger_mode"`
	Attempt     int            `json:"attempt"`
	MaxAttempts int            `json:"max_attempts"`
	Input       map[string]any `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at,omitempty"`
}

type Step struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	NodeID      string         `json:"node_id"`
	NodeType    string         `json:"node_type"`
	Status      StepStatus     `json:"status"`
	Attempt     int            `json:"attempt"`
	Input       map[string]any `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at,omitempty"`
}

type pendingState struct {
	WaitingNodeID string
	Queue         []string
	StepOutputs   map[string]any
	Executed      map[string]bool
}

type Repository interface {
	CreateRun(ctx context.Context, run Run) error
	UpdateRun(ctx context.Context, run Run) error
	CompleteRun(ctx context.Context, run Run, steps []Step) error
	SavePending(ctx context.Context, run Run, steps []Step, pending pendingState) error
	LoadPending(ctx context.Context, tenantID, runID string) (Run, []Step, pendingState, error)
	GetRun(ctx context.Context, tenantID, runID string) (Run, error)
	ListRuns(ctx context.Context, tenantID string, status RunStatus) ([]Run, error)
	ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error)
}

type LLMProvider interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type AsyncJob struct {
	RunID       string         `json:"run_id"`
	TenantID    string         `json:"tenant_id"`
	WorkflowID  string         `json:"workflow_id"`
	Attempt     int            `json:"attempt"`
	MaxAttempts int            `json:"max_attempts"`
	Input       map[string]any `json:"input,omitempty"`
}

type JobQueue interface {
	Enqueue(ctx context.Context, job AsyncJob) error
	EnqueueAfter(ctx context.Context, job AsyncJob, delay time.Duration) error
	Dequeue(ctx context.Context, wait time.Duration) (AsyncJob, bool, error)
}

type MemoryRepository struct {
	mu      sync.RWMutex
	runs    map[string]Run
	steps   map[string][]Step
	pending map[string]pendingState
}

type MockLLMProvider struct{}

type Service struct {
	repository Repository
	workflows  *workflow.Service
	documents  *documents.Service
	audit      *audit.Service
	trigger    *tenant.TriggerControl
	llm        LLMProvider
	httpClient HTTPClient
	jobQueue   JobQueue
	now        func() time.Time
	newID      func() (string, error)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		runs:    make(map[string]Run),
		steps:   make(map[string][]Step),
		pending: make(map[string]pendingState),
	}
}

func NewMockLLMProvider() MockLLMProvider {
	return MockLLMProvider{}
}

func NewService(repository Repository, workflows *workflow.Service, documents *documents.Service, llm LLMProvider) *Service {
	return &Service{
		repository: repository,
		workflows:  workflows,
		documents:  documents,
		llm:        llm,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		now:        func() time.Time { return time.Now().UTC() },
		newID:      ids.New,
	}
}

func (s *Service) WithHTTPClient(client HTTPClient) *Service {
	if client != nil {
		s.httpClient = client
	}

	return s
}

func (s *Service) WithJobQueue(queue JobQueue) *Service {
	if queue != nil {
		s.jobQueue = queue
	}

	return s
}

func (s *Service) WithAudit(auditService *audit.Service) *Service {
	if auditService != nil {
		s.audit = auditService
	}

	return s
}

func (s *Service) WithTriggerControl(control *tenant.TriggerControl) *Service {
	if control != nil {
		s.trigger = control
	}

	return s
}

func (r *MemoryRepository) CreateRun(_ context.Context, run Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runs[run.ID] = run
	return nil
}

func (r *MemoryRepository) CompleteRun(_ context.Context, run Run, steps []Step) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runs[run.ID] = run
	r.steps[run.ID] = append([]Step(nil), steps...)
	delete(r.pending, run.ID)
	return nil
}

func (r *MemoryRepository) UpdateRun(_ context.Context, run Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runs[run.ID] = run
	return nil
}

func (r *MemoryRepository) SavePending(_ context.Context, run Run, steps []Step, pending pendingState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runs[run.ID] = run
	r.steps[run.ID] = append([]Step(nil), steps...)
	r.pending[run.ID] = clonePendingState(pending)
	return nil
}

func (r *MemoryRepository) LoadPending(_ context.Context, tenantID, runID string) (Run, []Step, pendingState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	run, ok := r.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, nil, pendingState{}, ErrNotFound
	}

	pending, ok := r.pending[runID]
	if !ok {
		return Run{}, nil, pendingState{}, ErrRunNotAwaitingReview
	}

	return run, append([]Step(nil), r.steps[runID]...), clonePendingState(pending), nil
}

func (r *MemoryRepository) GetRun(_ context.Context, tenantID, runID string) (Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	run, ok := r.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}

	return run, nil
}

func (r *MemoryRepository) ListRuns(_ context.Context, tenantID string, status RunStatus) ([]Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	runs := make([]Run, 0, len(r.runs))
	for _, run := range r.runs {
		if run.TenantID != tenantID {
			continue
		}
		if status != "" && run.Status != status {
			continue
		}
		runs = append(runs, run)
	}

	slices.SortFunc(runs, func(left, right Run) int {
		switch {
		case left.CreatedAt.After(right.CreatedAt):
			return -1
		case left.CreatedAt.Before(right.CreatedAt):
			return 1
		default:
			return strings.Compare(left.ID, right.ID)
		}
	})

	return runs, nil
}

func (r *MemoryRepository) ListSteps(_ context.Context, tenantID, runID string) ([]Step, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	run, ok := r.runs[runID]
	if !ok || run.TenantID != tenantID {
		return nil, ErrNotFound
	}

	return append([]Step(nil), r.steps[runID]...), nil
}

func (m MockLLMProvider) Generate(_ context.Context, prompt string) (string, error) {
	return "mock-llm: " + strings.Join(strings.Fields(prompt), " "), nil
}

func (s *Service) ExecuteSync(ctx context.Context, tenantID, workflowID string, input map[string]any) (Run, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Run{}, ErrInvalidTenant
	}

	ctx, span := startTrace(ctx, "workflow.run.sync",
		attribute.String("tenant.id", tenantID),
		attribute.String("workflow.id", workflowID),
		attribute.String("run.trigger_mode", "sync"),
	)
	defer span.End()

	resolvedWorkflow, err := s.workflows.Get(ctx, tenantID, workflowID)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	runID, err := s.newID()
	if err != nil {
		recordTraceError(span, err)
		return Run{}, fmt.Errorf("generate run id: %w", err)
	}
	span.SetAttributes(attribute.String("run.id", runID))

	startedAt := s.now()
	run := Run{
		ID:          runID,
		TenantID:    tenantID,
		WorkflowID:  workflowID,
		Status:      RunStatusRunning,
		TriggerMode: "sync",
		Input:       cloneMap(input),
		CreatedAt:   startedAt,
		StartedAt:   startedAt,
	}
	if err := s.repository.CreateRun(ctx, run); err != nil {
		recordTraceError(span, err)
		return Run{}, fmt.Errorf("create run: %w", err)
	}

	graph, err := buildGraph(resolvedWorkflow.Definition)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	run, err = s.executeRun(ctx, run, graph, input, newExecutionState(graph.roots, len(resolvedWorkflow.Definition.Nodes)))
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	span.SetAttributes(attribute.String("run.status", string(run.Status)))
	return run, nil
}

func (s *Service) GetRun(ctx context.Context, tenantID, runID string) (Run, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Run{}, ErrInvalidTenant
	}

	return s.repository.GetRun(ctx, tenantID, runID)
}

func (s *Service) ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidTenant
	}

	return s.repository.ListSteps(ctx, tenantID, runID)
}

func (s *Service) ListRuns(ctx context.Context, tenantID string, status RunStatus) ([]Run, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidTenant
	}

	return s.repository.ListRuns(ctx, tenantID, status)
}

func (s *Service) ReserveExecution(tenantID string) (tenant.TriggerQuota, error) {
	if strings.TrimSpace(tenantID) == "" {
		return tenant.TriggerQuota{}, ErrInvalidTenant
	}
	if s.trigger == nil {
		return tenant.TriggerQuota{}, nil
	}

	return s.trigger.Allow(tenantID)
}

func (s *Service) LookupIdempotentRun(ctx context.Context, tenantID, key, fingerprint string) (Run, bool, error) {
	if s.trigger == nil {
		return Run{}, false, nil
	}

	record, ok, err := s.trigger.Lookup(tenantID, key, fingerprint)
	if err != nil || !ok {
		return Run{}, ok, err
	}

	run, err := s.repository.GetRun(ctx, tenantID, record.RunID)
	if err != nil {
		return Run{}, false, nil
	}

	return run, true, nil
}

func (s *Service) StoreIdempotentRun(tenantID, key, fingerprint string, run Run) error {
	if s.trigger == nil {
		return nil
	}

	return s.trigger.Store(tenantID, key, tenant.IdempotencyRecord{
		Fingerprint: fingerprint,
		RunID:       run.ID,
		TriggerMode: run.TriggerMode,
	})
}

func (s *Service) ResumeApproval(ctx context.Context, tenantID, runID string, approved bool, comment string, input map[string]any) (Run, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Run{}, ErrInvalidTenant
	}

	ctx, span := startTrace(ctx, "workflow.run.resume",
		attribute.String("tenant.id", tenantID),
		attribute.String("run.id", runID),
		attribute.Bool("approval.approved", approved),
	)
	defer span.End()

	run, steps, pending, err := s.repository.LoadPending(ctx, tenantID, runID)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}
	span.SetAttributes(attribute.String("workflow.id", run.WorkflowID))

	resolvedWorkflow, err := s.workflows.Get(ctx, tenantID, run.WorkflowID)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	graph, err := buildGraph(resolvedWorkflow.Definition)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	run.Input = mergeInput(run.Input, input)
	if len(steps) == 0 || steps[len(steps)-1].NodeID != pending.WaitingNodeID {
		recordTraceError(span, ErrRunNotAwaitingReview)
		return Run{}, ErrRunNotAwaitingReview
	}

	approvalOutput := map[string]any{
		"approved": approved,
		"comment":  strings.TrimSpace(comment),
	}
	steps[len(steps)-1].Status = StepStatusCompleted
	steps[len(steps)-1].Output = approvalOutput
	steps[len(steps)-1].CompletedAt = s.now()
	s.recordAudit(ctx, tenantID, audit.RecordParams{
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		StepID:     steps[len(steps)-1].ID,
		NodeID:     pending.WaitingNodeID,
		Type:       "approval_resolved",
		Message:    "approval decision recorded",
		Metadata:   approvalOutput,
	})

	if !approved {
		run.Status = RunStatusFailed
		run.Error = "approval rejected"
		run.Output = map[string]any{
			"failed_node": pending.WaitingNodeID,
			"approval":    approvalOutput,
		}
		run.CompletedAt = s.now()
		if err := s.repository.CompleteRun(ctx, run, steps); err != nil {
			recordTraceError(span, err)
			return Run{}, fmt.Errorf("complete rejected run: %w", err)
		}
		s.recordAudit(ctx, tenantID, newRunAudit(run, "run_failed", run.Error, map[string]any{
			"failed_node": pending.WaitingNodeID,
			"approval":    approvalOutput,
		}))

		span.SetAttributes(attribute.String("run.status", string(run.Status)))
		return run, nil
	}

	state := stateFromPending(steps, pending)
	state.stepOutputs[pending.WaitingNodeID] = approvalOutput
	state.executed[pending.WaitingNodeID] = true
	state.lastNodeID = pending.WaitingNodeID

	run.Status = RunStatusRunning
	run.Error = ""
	run.Output = nil
	run.CompletedAt = time.Time{}

	run, err = s.executeRun(ctx, run, graph, run.Input, state)
	if err != nil {
		recordTraceError(span, err)
		return Run{}, err
	}

	span.SetAttributes(attribute.String("run.status", string(run.Status)))
	return run, nil
}

func (s *Service) executeStep(ctx context.Context, runID, tenantID string, node workflow.Node, workflowInput map[string]any, stepOutputs map[string]any) (Step, any, error) {
	ctx, span := startTrace(ctx, "workflow.step", nodeTraceAttributes(runID, tenantID, node)...)
	defer span.End()

	stepID, err := s.newID()
	if err != nil {
		recordTraceError(span, err)
		return Step{}, nil, fmt.Errorf("generate step id: %w", err)
	}
	span.SetAttributes(attribute.String("step.id", stepID))

	step := Step{
		ID:        stepID,
		RunID:     runID,
		NodeID:    node.ID,
		NodeType:  node.Type,
		Status:    StepStatusCompleted,
		Attempt:   1,
		Input:     map[string]any{"config": node.Config},
		StartedAt: s.now(),
	}

	output, execErr := s.executeNode(ctx, tenantID, node, workflowInput, stepOutputs)
	step.CompletedAt = s.now()
	if execErr != nil {
		step.Status = StepStatusFailed
		step.Error = execErr.Error()
		recordTraceError(span, execErr)
		return step, nil, execErr
	}

	step.Output = output
	span.SetAttributes(attribute.String("step.status", string(step.Status)))
	return step, output, nil
}

func (s *Service) recordAudit(ctx context.Context, tenantID string, params audit.RecordParams) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, tenantID, params)
}

func (s *Service) executeNode(ctx context.Context, tenantID string, node workflow.Node, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	switch node.Type {
	case "retrieve_documents":
		return s.executeRetrieval(ctx, tenantID, node.Config, workflowInput)
	case "llm":
		return s.executeLLM(ctx, node.Config, workflowInput, stepOutputs)
	case "condition":
		return executeCondition(node.Config, workflowInput, stepOutputs)
	case "http_tool":
		return s.executeHTTPTool(ctx, node.Config, workflowInput, stepOutputs)
	case "audit_log":
		return executeAudit(node.Config, stepOutputs), nil
	default:
		return nil, fmt.Errorf("node type %q is not supported by sync execution yet", node.Type)
	}
}
