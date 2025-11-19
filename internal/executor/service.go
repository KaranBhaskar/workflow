package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"workflow/internal/documents"
	"workflow/internal/platform/ids"
	"workflow/internal/workflow"
)

var (
	ErrNotFound      = errors.New("workflow run not found")
	ErrInvalidTenant = errors.New("invalid tenant")
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type StepStatus string

const (
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
)

type Run struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	WorkflowID  string         `json:"workflow_id"`
	Status      RunStatus      `json:"status"`
	TriggerMode string         `json:"trigger_mode"`
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

type Repository interface {
	CreateRun(ctx context.Context, run Run) error
	CompleteRun(ctx context.Context, run Run, steps []Step) error
	GetRun(ctx context.Context, tenantID, runID string) (Run, error)
	ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error)
}

type LLMProvider interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type MemoryRepository struct {
	mu    sync.RWMutex
	runs  map[string]Run
	steps map[string][]Step
}

type MockLLMProvider struct{}

type Service struct {
	repository Repository
	workflows  *workflow.Service
	documents  *documents.Service
	llm        LLMProvider
	now        func() time.Time
	newID      func() (string, error)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		runs:  make(map[string]Run),
		steps: make(map[string][]Step),
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
		now:        func() time.Time { return time.Now().UTC() },
		newID:      ids.New,
	}
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
	return nil
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

	resolvedWorkflow, err := s.workflows.Get(ctx, tenantID, workflowID)
	if err != nil {
		return Run{}, err
	}

	runID, err := s.newID()
	if err != nil {
		return Run{}, fmt.Errorf("generate run id: %w", err)
	}

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
		return Run{}, fmt.Errorf("create run: %w", err)
	}

	nodes, err := topologicalNodes(resolvedWorkflow.Definition)
	if err != nil {
		return Run{}, err
	}

	steps := make([]Step, 0, len(nodes))
	stepOutputs := make(map[string]any, len(nodes))
	for _, node := range nodes {
		step, output, execErr := s.executeStep(ctx, run.ID, tenantID, node, input, stepOutputs)
		steps = append(steps, step)
		if execErr != nil {
			run.Status = RunStatusFailed
			run.Error = execErr.Error()
			run.Output = map[string]any{"failed_node": node.ID}
			run.CompletedAt = s.now()
			if completeErr := s.repository.CompleteRun(ctx, run, steps); completeErr != nil {
				return Run{}, fmt.Errorf("complete failed run: %w", completeErr)
			}

			return run, nil
		}

		stepOutputs[node.ID] = output
	}

	run.Status = RunStatusCompleted
	run.Output = buildRunOutput(nodes, stepOutputs)
	run.CompletedAt = s.now()
	if err := s.repository.CompleteRun(ctx, run, steps); err != nil {
		return Run{}, fmt.Errorf("complete run: %w", err)
	}

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

func (s *Service) executeStep(ctx context.Context, runID, tenantID string, node workflow.Node, workflowInput map[string]any, stepOutputs map[string]any) (Step, any, error) {
	stepID, err := s.newID()
	if err != nil {
		return Step{}, nil, fmt.Errorf("generate step id: %w", err)
	}

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
		return step, nil, execErr
	}

	step.Output = output
	return step, output, nil
}

func (s *Service) executeNode(ctx context.Context, tenantID string, node workflow.Node, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	switch node.Type {
	case "retrieve_documents":
		return s.executeRetrieval(ctx, tenantID, node.Config, workflowInput)
	case "llm":
		return s.executeLLM(ctx, node.Config, workflowInput, stepOutputs)
	case "audit_log":
		return executeAudit(node.Config, stepOutputs), nil
	default:
		return nil, fmt.Errorf("node type %q is not supported by sync execution yet", node.Type)
	}
}

func (s *Service) executeRetrieval(ctx context.Context, tenantID string, config map[string]any, workflowInput map[string]any) (any, error) {
	documentIDs := stringSliceConfig(config, "document_ids")
	if len(documentIDs) == 0 {
		return nil, errors.New("retrieve_documents requires document_ids")
	}

	query := strings.ToLower(strings.TrimSpace(anyString(workflowInput[stringConfig(config, "query_input_key", "query")])))
	limit := intConfig(config, "limit", 3)

	type scoredChunk struct {
		chunk documents.Chunk
		score int
	}

	results := make([]scoredChunk, 0)
	for _, documentID := range documentIDs {
		chunks, err := s.documents.ListChunks(ctx, tenantID, documentID)
		if err != nil {
			return nil, err
		}

		for _, chunk := range chunks {
			score := lexicalScore(query, chunk.Content)
			if query != "" && score == 0 {
				continue
			}
			results = append(results, scoredChunk{chunk: chunk, score: score})
		}
	}

	slices.SortFunc(results, func(left, right scoredChunk) int {
		switch {
		case left.score != right.score:
			return right.score - left.score
		case left.chunk.DocumentID != right.chunk.DocumentID:
			return strings.Compare(left.chunk.DocumentID, right.chunk.DocumentID)
		default:
			return left.chunk.ChunkIndex - right.chunk.ChunkIndex
		}
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	matches := make([]documents.Chunk, 0, len(results))
	for _, result := range results {
		matches = append(matches, result.chunk)
	}

	return map[string]any{
		"query":     query,
		"documents": matches,
	}, nil
}

func (s *Service) executeLLM(ctx context.Context, config map[string]any, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	parts := make([]string, 0, 3)
	if prompt := stringConfig(config, "prompt", ""); prompt != "" {
		parts = append(parts, prompt)
	}
	if inputKey := stringConfig(config, "input_key", ""); inputKey != "" {
		if inputValue := anyString(workflowInput[inputKey]); inputValue != "" {
			parts = append(parts, inputValue)
		}
	}
	if contextStep := stringConfig(config, "context_step", ""); contextStep != "" {
		if contextValue, ok := stepOutputs[contextStep]; ok {
			parts = append(parts, renderAny(contextValue))
		}
	}

	prompt := strings.Join(parts, "\n\n")
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"prompt": prompt,
		"text":   response,
	}, nil
}

func executeAudit(config map[string]any, stepOutputs map[string]any) any {
	result := map[string]any{
		"message": stringConfig(config, "message", "audit logged"),
	}
	if fromStep := stringConfig(config, "from_step", ""); fromStep != "" {
		result["from_step"] = fromStep
		result["value"] = stepOutputs[fromStep]
	}

	return result
}

func buildRunOutput(nodes []workflow.Node, stepOutputs map[string]any) any {
	if len(nodes) == 0 {
		return nil
	}

	lastNode := nodes[len(nodes)-1]
	return map[string]any{
		"last_node": lastNode.ID,
		"result":    stepOutputs[lastNode.ID],
	}
}

func topologicalNodes(definition workflow.Definition) ([]workflow.Node, error) {
	nodeByID := make(map[string]workflow.Node, len(definition.Nodes))
	inDegree := make(map[string]int, len(definition.Nodes))
	outbound := make(map[string][]string, len(definition.Nodes))

	for _, node := range definition.Nodes {
		nodeByID[node.ID] = node
		inDegree[node.ID] = 0
	}
	for _, edge := range definition.Edges {
		outbound[edge.From] = append(outbound[edge.From], edge.To)
		inDegree[edge.To]++
	}

	queue := make([]string, 0, len(definition.Nodes))
	queued := make(map[string]bool, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if inDegree[node.ID] == 0 {
			queue = append(queue, node.ID)
			queued[node.ID] = true
		}
	}

	ordered := make([]workflow.Node, 0, len(definition.Nodes))
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		ordered = append(ordered, nodeByID[currentID])

		for _, nextID := range outbound[currentID] {
			inDegree[nextID]--
		}
		for _, node := range definition.Nodes {
			if inDegree[node.ID] == 0 && !queued[node.ID] {
				queue = append(queue, node.ID)
				queued[node.ID] = true
			}
		}
	}

	if len(ordered) != len(definition.Nodes) {
		return nil, errors.New("workflow contains a cycle and cannot be executed")
	}

	return ordered, nil
}

func lexicalScore(query, content string) int {
	if query == "" {
		return 1
	}

	score := 0
	lowerContent := strings.ToLower(content)
	for _, term := range strings.Fields(query) {
		if strings.Contains(lowerContent, term) {
			score++
		}
	}

	return score
}

func stringSliceConfig(config map[string]any, key string) []string {
	values, ok := config[key]
	if !ok {
		return nil
	}

	switch typed := values.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(anyString(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringConfig(config map[string]any, key, fallback string) string {
	if value, ok := config[key]; ok {
		if text := strings.TrimSpace(anyString(value)); text != "" {
			return text
		}
	}

	return fallback
}

func intConfig(config map[string]any, key string, fallback int) int {
	value, ok := config[key]
	if !ok {
		return fallback
	}

	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return renderAny(value)
	}
}

func renderAny(value any) string {
	if value == nil {
		return ""
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	return string(encoded)
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
