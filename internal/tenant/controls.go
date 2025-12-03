package tenant

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrRateLimited         = errors.New("tenant execution rate limit exceeded")
	ErrIdempotencyConflict = errors.New("idempotency key already used with different request")
)

type TriggerQuota struct {
	Limit     int
	Remaining int
	ResetAt   time.Time
}

type IdempotencyRecord struct {
	Fingerprint string
	RunID       string
	TriggerMode string
}

type TriggerControl struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	quotas  map[string]quotaBucket
	records map[string]IdempotencyRecord
}

type quotaBucket struct {
	windowStart time.Time
	count       int
}

func NewTriggerControl(limit int, window time.Duration) *TriggerControl {
	if limit < 0 {
		limit = 0
	}
	if window <= 0 {
		window = time.Minute
	}

	return &TriggerControl{
		limit:   limit,
		window:  window,
		now:     func() time.Time { return time.Now().UTC() },
		quotas:  make(map[string]quotaBucket),
		records: make(map[string]IdempotencyRecord),
	}
}

func (c *TriggerControl) WithClock(now func() time.Time) *TriggerControl {
	if now != nil {
		c.now = now
	}

	return c
}

func (c *TriggerControl) Allow(tenantID string) (TriggerQuota, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.limit == 0 {
		return TriggerQuota{}, nil
	}

	now := c.now()
	bucket := c.quotas[tenantID]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= c.window {
		bucket = quotaBucket{windowStart: now}
	}

	resetAt := bucket.windowStart.Add(c.window)
	if bucket.count >= c.limit {
		return TriggerQuota{
			Limit:     c.limit,
			Remaining: 0,
			ResetAt:   resetAt,
		}, ErrRateLimited
	}

	bucket.count++
	c.quotas[tenantID] = bucket

	return TriggerQuota{
		Limit:     c.limit,
		Remaining: c.limit - bucket.count,
		ResetAt:   resetAt,
	}, nil
}

func (c *TriggerControl) Lookup(tenantID, key, fingerprint string) (IdempotencyRecord, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return IdempotencyRecord{}, false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.records[c.recordKey(tenantID, key)]
	if !ok {
		return IdempotencyRecord{}, false, nil
	}
	if record.Fingerprint != fingerprint {
		return IdempotencyRecord{}, false, ErrIdempotencyConflict
	}

	return record, true, nil
}

func (c *TriggerControl) Store(tenantID, key string, record IdempotencyRecord) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	storageKey := c.recordKey(tenantID, key)
	if existing, ok := c.records[storageKey]; ok && existing.Fingerprint != record.Fingerprint {
		return ErrIdempotencyConflict
	}
	c.records[storageKey] = record
	return nil
}

func (c *TriggerControl) recordKey(tenantID, key string) string {
	return tenantID + ":" + key
}
