package unit_test

import (
	"strings"
	"testing"
	"time"

	"workflow/internal/benchmark"
)

func TestSummarizeLatenciesComputesPercentiles(t *testing.T) {
	t.Parallel()

	summary := benchmark.SummarizeLatencies([]time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	})

	if summary.Min != 10*time.Millisecond {
		t.Fatalf("expected min latency 10ms, got %s", summary.Min)
	}
	if summary.P50 != 30*time.Millisecond {
		t.Fatalf("expected p50 latency 30ms, got %s", summary.P50)
	}
	if summary.P95 != 50*time.Millisecond {
		t.Fatalf("expected p95 latency 50ms, got %s", summary.P95)
	}
	if summary.Max != 50*time.Millisecond {
		t.Fatalf("expected max latency 50ms, got %s", summary.Max)
	}
}

func TestSummarizeExecutionTracksRetryAndDeadLetterRates(t *testing.T) {
	t.Parallel()

	summary := benchmark.SummarizeExecution([]benchmark.RunSample{
		{Duration: 20 * time.Millisecond, Completed: true},
		{Duration: 25 * time.Millisecond, Completed: true, Retried: true},
		{Duration: 40 * time.Millisecond, DeadLetter: true, Retried: true},
	}, 100*time.Millisecond)

	if summary.Runs != 3 {
		t.Fatalf("expected 3 runs, got %d", summary.Runs)
	}
	if summary.Completed != 2 {
		t.Fatalf("expected 2 completed runs, got %d", summary.Completed)
	}
	if summary.DeadLetters != 1 {
		t.Fatalf("expected 1 dead-letter run, got %d", summary.DeadLetters)
	}
	if summary.Retried != 2 {
		t.Fatalf("expected 2 retried runs, got %d", summary.Retried)
	}
	if summary.SuccessRate < 66 || summary.SuccessRate > 67 {
		t.Fatalf("expected success rate near 66.67, got %.2f", summary.SuccessRate)
	}
	if summary.RetryRate < 66 || summary.RetryRate > 67 {
		t.Fatalf("expected retry rate near 66.67, got %.2f", summary.RetryRate)
	}
}

func TestRenderMarkdownIncludesKeyBenchmarkMetrics(t *testing.T) {
	t.Parallel()

	output := benchmark.RenderMarkdown(benchmark.Result{
		GeneratedAt: time.Date(2026, time.March, 19, 15, 4, 5, 0, time.UTC),
		Dataset: benchmark.Dataset{
			Tenants:            4,
			Documents:          800,
			DocumentsPerTenant: 200,
			DocumentWords:      900,
			SyncRuns:           1200,
			AsyncRuns:          800,
			Concurrency:        24,
			RetryPercent:       12,
			DeadLetterPercent:  3,
		},
		Ingestion: benchmark.IngestionSummary{
			Documents:  800,
			Bytes:      1024,
			Duration:   2 * time.Second,
			Throughput: 400,
		},
		Sync: benchmark.ExecutionSummary{
			Runs:       1200,
			Completed:  1200,
			Duration:   3 * time.Second,
			Throughput: 400,
			Latency: benchmark.LatencySummary{
				P50: 12 * time.Millisecond,
				P95: 25 * time.Millisecond,
			},
		},
		Async: benchmark.ExecutionSummary{
			Runs:        800,
			Completed:   760,
			DeadLetters: 40,
			Retried:     120,
			Duration:    5 * time.Second,
			Throughput:  160,
			Latency: benchmark.LatencySummary{
				P50: 20 * time.Millisecond,
				P95: 45 * time.Millisecond,
			},
		},
		Overall: benchmark.ExecutionSummary{
			Runs:       2000,
			Completed:  1960,
			Retried:    120,
			Duration:   8 * time.Second,
			Throughput: 250,
			Latency: benchmark.LatencySummary{
				P50: 15 * time.Millisecond,
				P95: 40 * time.Millisecond,
			},
		},
	})

	for _, fragment := range []string{
		"# Benchmark Results",
		"documents: 800",
		"ingestion throughput: 400.00 docs/sec",
		"### Sync",
		"### Async",
		"latency p95: 40ms",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected markdown to contain %q", fragment)
		}
	}
}
