package worker

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"workflow/internal/executor"
)

type Service struct {
	logger      *slog.Logger
	queue       executor.JobQueue
	executor    *executor.Service
	pollTimeout time.Duration
	baseBackoff time.Duration
}

func NewService(logger *slog.Logger, queue executor.JobQueue, executorService *executor.Service) *Service {
	return &Service{
		logger:      logger,
		queue:       queue,
		executor:    executorService,
		pollTimeout: 250 * time.Millisecond,
		baseBackoff: 50 * time.Millisecond,
	}
}

func (s *Service) WithPollTimeout(timeout time.Duration) *Service {
	if timeout > 0 {
		s.pollTimeout = timeout
	}

	return s
}

func (s *Service) ProcessNext(ctx context.Context) (bool, error) {
	job, ok, err := s.queue.Dequeue(ctx, s.pollTimeout)
	if err != nil || !ok {
		return false, err
	}

	ctx, span := otel.Tracer("workflow/internal/worker").Start(ctx, "worker.process_next")
	span.SetAttributes(
		attribute.String("tenant.id", job.TenantID),
		attribute.String("workflow.id", job.WorkflowID),
		attribute.String("run.id", job.RunID),
		attribute.Int("run.attempt", job.Attempt),
		attribute.Int("run.max_attempts", job.MaxAttempts),
	)
	defer span.End()

	run, err := s.executor.ProcessAsyncJob(ctx, job)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return true, err
	}
	if run.Status != executor.RunStatusFailed {
		span.SetAttributes(attribute.String("run.status", string(run.Status)))
		return true, nil
	}
	if run.Attempt < run.MaxAttempts {
		job.Attempt = run.Attempt
		job.MaxAttempts = run.MaxAttempts
		delay := s.retryDelay(run.Attempt)
		if _, err := s.executor.MarkAsyncRetry(ctx, run, delay); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return true, err
		}
		if err := s.queue.EnqueueAfter(ctx, job, delay); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return true, err
		}
		span.AddEvent("retry_scheduled", oteltrace.WithAttributes(attribute.Int64("retry.delay_ms", delay.Milliseconds())))
		span.SetAttributes(attribute.String("run.status", string(executor.RunStatusQueued)))

		return true, nil
	}

	_, err = s.executor.MarkDeadLetter(ctx, run)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return true, err
	}
	span.SetAttributes(attribute.String("run.status", string(executor.RunStatusDeadLetter)))
	return true, err
}

func (s *Service) Run(ctx context.Context) error {
	for {
		processed, err := s.ProcessNext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if s.logger != nil {
				s.logger.Error("async worker failed to process job", "error", err)
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		if !processed && ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *Service) retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return s.baseBackoff
	}

	return s.baseBackoff * time.Duration(1<<(attempt-1))
}
