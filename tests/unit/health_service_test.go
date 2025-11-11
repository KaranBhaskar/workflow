package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workflow/internal/platform/health"
)

type fakeChecker struct {
	name  string
	err   error
	delay time.Duration
}

func (f fakeChecker) Name() string {
	return f.name
}

func (f fakeChecker) Check(ctx context.Context) error {
	if f.delay == 0 {
		return f.err
	}

	timer := time.NewTimer(f.delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return f.err
	}
}

func TestLiveReportIsAlwaysHealthy(t *testing.T) {
	t.Parallel()

	service := health.NewService("workflow-api", time.Second)
	report := service.LiveReport()

	if report.Status != health.StatusLive {
		t.Fatalf("expected %q, got %q", health.StatusLive, report.Status)
	}

	if report.Service != "workflow-api" {
		t.Fatalf("expected service name to be propagated, got %q", report.Service)
	}
}

func TestReadyReportReturnsReadyWhenAllChecksPass(t *testing.T) {
	t.Parallel()

	service := health.NewService(
		"workflow-api",
		time.Second,
		fakeChecker{name: "postgres"},
		fakeChecker{name: "redis"},
	)

	report, ready := service.ReadyReport(context.Background())
	if !ready {
		t.Fatal("expected readiness to be healthy")
	}

	if report.Status != health.StatusReady {
		t.Fatalf("expected %q, got %q", health.StatusReady, report.Status)
	}

	if len(report.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(report.Checks))
	}
}

func TestReadyReportReturnsNotReadyWhenCheckFails(t *testing.T) {
	t.Parallel()

	service := health.NewService(
		"workflow-api",
		time.Second,
		fakeChecker{name: "postgres"},
		fakeChecker{name: "redis", err: errors.New("connection refused")},
	)

	report, ready := service.ReadyReport(context.Background())
	if ready {
		t.Fatal("expected readiness to fail")
	}

	if report.Status != health.StatusNotReady {
		t.Fatalf("expected %q, got %q", health.StatusNotReady, report.Status)
	}

	if len(report.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(report.Checks))
	}

	if report.Checks[1].Error == "" {
		t.Fatal("expected failed check to include an error message")
	}
}
