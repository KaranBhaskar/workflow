package executor

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) ExecuteAsync(ctx context.Context, tenantID, workflowID string, input map[string]any) (Run, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Run{}, ErrInvalidTenant
	}
	if s.jobQueue == nil {
		return Run{}, ErrAsyncDisabled
	}
	if _, err := s.workflows.Get(ctx, tenantID, workflowID); err != nil {
		return Run{}, err
	}

	runID, err := s.newID()
	if err != nil {
		return Run{}, fmt.Errorf("generate run id: %w", err)
	}

	createdAt := s.now()
	run := Run{
		ID:          runID,
		TenantID:    tenantID,
		WorkflowID:  workflowID,
		Status:      RunStatusQueued,
		TriggerMode: "async",
		Input:       cloneMap(input),
		CreatedAt:   createdAt,
	}
	if err := s.repository.CreateRun(ctx, run); err != nil {
		return Run{}, fmt.Errorf("create async run: %w", err)
	}

	job := AsyncJob{
		RunID:      run.ID,
		TenantID:   tenantID,
		WorkflowID: workflowID,
		Input:      cloneMap(input),
	}
	if err := s.jobQueue.Enqueue(ctx, job); err != nil {
		run.Status = RunStatusFailed
		run.Error = err.Error()
		run.CompletedAt = s.now()
		if completeErr := s.repository.CompleteRun(ctx, run, nil); completeErr != nil {
			return Run{}, fmt.Errorf("enqueue async run: %w (mark failed: %v)", err, completeErr)
		}

		return Run{}, fmt.Errorf("enqueue async run: %w", err)
	}

	return run, nil
}

func (s *Service) ProcessAsyncJob(ctx context.Context, job AsyncJob) (Run, error) {
	if strings.TrimSpace(job.TenantID) == "" {
		return Run{}, ErrInvalidTenant
	}

	run, err := s.repository.GetRun(ctx, job.TenantID, job.RunID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != RunStatusQueued {
		return run, nil
	}

	resolvedWorkflow, err := s.workflows.Get(ctx, job.TenantID, job.WorkflowID)
	if err != nil {
		run.Status = RunStatusFailed
		run.Error = err.Error()
		run.CompletedAt = s.now()
		if completeErr := s.repository.CompleteRun(ctx, run, nil); completeErr != nil {
			return Run{}, fmt.Errorf("complete missing async workflow run: %w", completeErr)
		}

		return run, nil
	}

	graph, err := buildGraph(resolvedWorkflow.Definition)
	if err != nil {
		run.Status = RunStatusFailed
		run.Error = err.Error()
		run.CompletedAt = s.now()
		if completeErr := s.repository.CompleteRun(ctx, run, nil); completeErr != nil {
			return Run{}, fmt.Errorf("complete invalid async workflow run: %w", completeErr)
		}

		return run, nil
	}

	run.Status = RunStatusRunning
	run.Input = mergeInput(run.Input, job.Input)
	run.StartedAt = s.now()
	if err := s.repository.UpdateRun(ctx, run); err != nil {
		return Run{}, fmt.Errorf("update async run: %w", err)
	}

	return s.executeRun(ctx, run, graph, run.Input, newExecutionState(graph.roots, len(resolvedWorkflow.Definition.Nodes)))
}

func (s *Service) executeRun(ctx context.Context, run Run, graph graph, input map[string]any, state executionState) (Run, error) {
	state, pause, failure, err := s.continueExecution(ctx, run.ID, run.TenantID, graph, input, state)
	if err != nil {
		return Run{}, err
	}
	if failure != nil {
		run.Status = RunStatusFailed
		run.Error = failure.Err.Error()
		run.Output = map[string]any{"failed_node": failure.NodeID}
		run.CompletedAt = s.now()
		if err := s.repository.CompleteRun(ctx, run, state.steps); err != nil {
			return Run{}, fmt.Errorf("complete failed run: %w", err)
		}

		return run, nil
	}
	if pause != nil {
		run.Status = RunStatusWaitingApproval
		run.Output = pause.output
		if err := s.repository.SavePending(ctx, run, state.steps, pause.pending); err != nil {
			return Run{}, fmt.Errorf("save pending run: %w", err)
		}

		return run, nil
	}

	run.Status = RunStatusCompleted
	run.Output = buildRunOutput(state.lastNodeID, state.stepOutputs)
	run.CompletedAt = s.now()
	if err := s.repository.CompleteRun(ctx, run, state.steps); err != nil {
		return Run{}, fmt.Errorf("complete run: %w", err)
	}

	return run, nil
}
