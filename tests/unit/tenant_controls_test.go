package unit_test

import (
	"testing"
	"time"

	"workflow/internal/tenant"
)

func TestTriggerControlResetsQuotaWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	control := tenant.NewTriggerControl(1, time.Minute).WithClock(func() time.Time { return now })

	quota, err := control.Allow("tenant-a")
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if quota.Remaining != 0 {
		t.Fatalf("expected remaining 0, got %d", quota.Remaining)
	}

	if _, err := control.Allow("tenant-a"); err == nil {
		t.Fatal("expected rate limit error")
	}

	now = now.Add(2 * time.Minute)
	if _, err := control.Allow("tenant-a"); err != nil {
		t.Fatalf("expected rate limit window reset, got %v", err)
	}
}

func TestTriggerControlDetectsIdempotencyConflicts(t *testing.T) {
	t.Parallel()

	control := tenant.NewTriggerControl(5, time.Minute)
	if err := control.Store("tenant-a", "key-1", tenant.IdempotencyRecord{
		Fingerprint: `{"mode":"sync","workflow_id":"wf-1"}`,
		RunID:       "run-1",
		TriggerMode: "sync",
	}); err != nil {
		t.Fatalf("store record: %v", err)
	}

	if _, ok, err := control.Lookup("tenant-a", "key-1", `{"mode":"sync","workflow_id":"wf-1"}`); err != nil || !ok {
		t.Fatalf("expected idempotency replay, got ok=%v err=%v", ok, err)
	}

	if _, _, err := control.Lookup("tenant-a", "key-1", `{"mode":"async","workflow_id":"wf-1"}`); err == nil {
		t.Fatal("expected idempotency conflict")
	}
}
