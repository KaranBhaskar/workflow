package unit_test

import (
	"context"
	"testing"
	"time"

	"workflow/internal/audit"
)

func TestListRunEventsScopesByTenantAndSortsChronologically(t *testing.T) {
	t.Parallel()

	service := audit.NewService(audit.NewMemoryRepository())
	serviceNow := []time.Time{
		time.Unix(20, 0).UTC(),
		time.Unix(10, 0).UTC(),
		time.Unix(30, 0).UTC(),
	}
	index := 0
	service.WithClock(func() time.Time {
		current := serviceNow[index]
		index++
		return current
	})

	if err := service.Record(context.Background(), "tenant-a", audit.RecordParams{RunID: "run-1", Type: "run_completed"}); err != nil {
		t.Fatalf("record first event: %v", err)
	}
	if err := service.Record(context.Background(), "tenant-b", audit.RecordParams{RunID: "run-1", Type: "run_failed"}); err != nil {
		t.Fatalf("record second event: %v", err)
	}
	if err := service.Record(context.Background(), "tenant-a", audit.RecordParams{RunID: "run-1", Type: "run_started"}); err != nil {
		t.Fatalf("record third event: %v", err)
	}

	events, err := service.ListRunEvents(context.Background(), "tenant-a", "run-1")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 tenant-scoped events, got %d", len(events))
	}
	if events[0].Type != "run_completed" || events[1].Type != "run_started" {
		t.Fatalf("expected chronological ordering, got %q then %q", events[0].Type, events[1].Type)
	}
}
