package executor

import (
	"context"
	"fmt"

	"workflow/internal/audit"
	"workflow/internal/workflow"
)

type executionState struct {
	steps       []Step
	stepOutputs map[string]any
	lastNodeID  string
	queue       []string
	executed    map[string]bool
}

type pausePoint struct {
	output  any
	pending pendingState
}

type runFailure struct {
	NodeID string
	Err    error
}

func newExecutionState(roots []string, capacity int) executionState {
	return executionState{
		steps:       make([]Step, 0, capacity),
		stepOutputs: make(map[string]any, capacity),
		queue:       append([]string(nil), roots...),
		executed:    make(map[string]bool, capacity),
	}
}

func stateFromPending(steps []Step, pending pendingState) executionState {
	stepOutputs := cloneMap(pending.StepOutputs)
	if stepOutputs == nil {
		stepOutputs = make(map[string]any)
	}
	executed := cloneBoolMap(pending.Executed)
	if executed == nil {
		executed = make(map[string]bool)
	}

	return executionState{
		steps:       append([]Step(nil), steps...),
		stepOutputs: stepOutputs,
		queue:       append([]string(nil), pending.Queue...),
		executed:    executed,
	}
}

func (s *Service) continueExecution(ctx context.Context, runID, tenantID string, graph graph, workflowInput map[string]any, state executionState) (executionState, *pausePoint, *runFailure, error) {
	queued := make(map[string]bool, len(state.queue))
	for _, nodeID := range state.queue {
		queued[nodeID] = true
	}

	for len(state.queue) > 0 {
		nodeID := state.queue[0]
		state.queue = state.queue[1:]
		delete(queued, nodeID)

		node := graph.nodes[nodeID]
		if node.Type == "approval" {
			step, output, err := s.awaitApproval(runID, node)
			if err != nil {
				return state, nil, nil, err
			}

			state.steps = append(state.steps, step)
			state.lastNodeID = node.ID
			s.recordAudit(ctx, tenantID, auditParamsForStep("step_waiting_approval", "", step, map[string]any{
				"output": output,
			}))
			return state, &pausePoint{
				output: output,
				pending: pendingState{
					WaitingNodeID: node.ID,
					Queue:         appendQueue(state.queue, graph.next(node, output), state.executed),
					StepOutputs:   cloneMap(state.stepOutputs),
					Executed:      cloneBoolMap(state.executed),
				},
			}, nil, nil
		}

		step, output, execErr := s.executeStep(ctx, runID, tenantID, node, workflowInput, state.stepOutputs)
		state.steps = append(state.steps, step)
		state.lastNodeID = node.ID
		if execErr != nil {
			s.recordAudit(ctx, tenantID, auditParamsForStep("step_failed", execErr.Error(), step, map[string]any{
				"error": execErr.Error(),
			}))
			return state, nil, &runFailure{NodeID: node.ID, Err: execErr}, nil
		}

		s.recordAudit(ctx, tenantID, auditParamsForStep("step_completed", "", step, map[string]any{
			"output": output,
		}))
		state.executed[node.ID] = true
		state.stepOutputs[node.ID] = output
		for _, nextID := range graph.next(node, output) {
			if state.executed[nextID] || queued[nextID] {
				continue
			}
			state.queue = append(state.queue, nextID)
			queued[nextID] = true
		}
	}

	return state, nil, nil, nil
}

func (s *Service) awaitApproval(runID string, node workflow.Node) (Step, any, error) {
	stepID, err := s.newID()
	if err != nil {
		return Step{}, nil, fmt.Errorf("generate step id: %w", err)
	}

	output := map[string]any{
		"message": stringConfig(node.Config, "message", "approval required"),
	}
	step := Step{
		ID:        stepID,
		RunID:     runID,
		NodeID:    node.ID,
		NodeType:  node.Type,
		Status:    StepStatusWaitingApproval,
		Attempt:   1,
		Input:     map[string]any{"config": node.Config},
		Output:    output,
		StartedAt: s.now(),
	}

	return step, output, nil
}

func auditParamsForStep(eventType, message string, step Step, metadata map[string]any) audit.RecordParams {
	return audit.RecordParams{
		RunID:    step.RunID,
		StepID:   step.ID,
		NodeID:   step.NodeID,
		Type:     eventType,
		Message:  message,
		Metadata: metadata,
	}
}
