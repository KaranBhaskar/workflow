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
}

func NewService(logger *slog.Logger, queue executor.JobQueue, executorService *executor.Service) *Service {
	return &Service{
		logger:      logger,
		queue:       queue,
		executor:    executorService,
		pollTimeout: 250 * time.Millisecond,
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

	_, err = s.executor.ProcessAsyncJob(ctx, job)
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
