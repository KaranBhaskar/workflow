package worker

import (
	"context"
	"log/slog"
	"time"

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

	run, err := s.executor.ProcessAsyncJob(ctx, job)
	if err != nil {
		return true, err
	}
	if run.Status != executor.RunStatusFailed {
		return true, nil
	}
	if run.Attempt < run.MaxAttempts {
		job.Attempt = run.Attempt
		job.MaxAttempts = run.MaxAttempts
		delay := s.retryDelay(run.Attempt)
		if _, err := s.executor.MarkAsyncRetry(ctx, run, delay); err != nil {
			return true, err
		}
		if err := s.queue.EnqueueAfter(ctx, job, delay); err != nil {
			return true, err
		}

		return true, nil
	}

	_, err = s.executor.MarkDeadLetter(ctx, run)
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
