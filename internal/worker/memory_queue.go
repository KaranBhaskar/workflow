package worker

import (
	"context"
	"time"

	"workflow/internal/executor"
)

type MemoryQueue struct {
	jobs chan executor.AsyncJob
}

func NewMemoryQueue(size int) *MemoryQueue {
	if size <= 0 {
		size = 128
	}

	return &MemoryQueue{
		jobs: make(chan executor.AsyncJob, size),
	}
}

func (q *MemoryQueue) Enqueue(ctx context.Context, job executor.AsyncJob) error {
	select {
	case q.jobs <- cloneJob(job):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *MemoryQueue) EnqueueAfter(ctx context.Context, job executor.AsyncJob, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		<-timer.C
		q.jobs <- cloneJob(job)
	}()

	return nil
}

func (q *MemoryQueue) Dequeue(ctx context.Context, wait time.Duration) (executor.AsyncJob, bool, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case job := <-q.jobs:
		return cloneJob(job), true, nil
	case <-timer.C:
		return executor.AsyncJob{}, false, nil
	case <-ctx.Done():
		return executor.AsyncJob{}, false, ctx.Err()
	}
}

func cloneJob(job executor.AsyncJob) executor.AsyncJob {
	return executor.AsyncJob{
		RunID:       job.RunID,
		TenantID:    job.TenantID,
		WorkflowID:  job.WorkflowID,
		Attempt:     job.Attempt,
		MaxAttempts: job.MaxAttempts,
		Input:       cloneInput(job.Input),
	}
}

func cloneInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}
