package benchmark

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

type Dataset struct {
	Tenants            int `json:"tenants"`
	Documents          int `json:"documents"`
	DocumentsPerTenant int `json:"documents_per_tenant"`
	DocumentWords      int `json:"document_words"`
	SyncRuns           int `json:"sync_runs"`
	AsyncRuns          int `json:"async_runs"`
	Concurrency        int `json:"concurrency"`
	RetryPercent       int `json:"retry_percent"`
	DeadLetterPercent  int `json:"dead_letter_percent"`
}

type IngestionSummary struct {
	Documents  int           `json:"documents"`
	Bytes      int64         `json:"bytes"`
	Duration   time.Duration `json:"duration"`
	Throughput float64       `json:"throughput_docs_per_sec"`
}

type LatencySummary struct {
	Count int           `json:"count"`
	Min   time.Duration `json:"min"`
	P50   time.Duration `json:"p50"`
	P95   time.Duration `json:"p95"`
	Max   time.Duration `json:"max"`
	Mean  time.Duration `json:"mean"`
}

type RunSample struct {
	Duration   time.Duration
	Completed  bool
	DeadLetter bool
	Retried    bool
}

type ExecutionSummary struct {
	Runs        int            `json:"runs"`
	Completed   int            `json:"completed"`
	DeadLetters int            `json:"dead_letters"`
	Retried     int            `json:"retried"`
	Duration    time.Duration  `json:"duration"`
	Throughput  float64        `json:"throughput_runs_per_sec"`
	SuccessRate float64        `json:"success_rate"`
	RetryRate   float64        `json:"retry_rate"`
	Latency     LatencySummary `json:"latency"`
}

type Result struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Dataset     Dataset          `json:"dataset"`
	Ingestion   IngestionSummary `json:"ingestion"`
	Sync        ExecutionSummary `json:"sync"`
	Async       ExecutionSummary `json:"async"`
	Overall     ExecutionSummary `json:"overall"`
}

func SummarizeLatencies(durations []time.Duration) LatencySummary {
	if len(durations) == 0 {
		return LatencySummary{}
	}

	ordered := append([]time.Duration(nil), durations...)
	slices.Sort(ordered)

	var total time.Duration
	for _, duration := range ordered {
		total += duration
	}

	return LatencySummary{
		Count: len(ordered),
		Min:   ordered[0],
		P50:   percentile(ordered, 50),
		P95:   percentile(ordered, 95),
		Max:   ordered[len(ordered)-1],
		Mean:  total / time.Duration(len(ordered)),
	}
}

func SummarizeExecution(samples []RunSample, duration time.Duration) ExecutionSummary {
	latencies := make([]time.Duration, 0, len(samples))
	summary := ExecutionSummary{
		Runs:     len(samples),
		Duration: duration,
	}

	for _, sample := range samples {
		latencies = append(latencies, sample.Duration)
		if sample.Completed {
			summary.Completed++
		}
		if sample.DeadLetter {
			summary.DeadLetters++
		}
		if sample.Retried {
			summary.Retried++
		}
	}

	summary.Latency = SummarizeLatencies(latencies)
	summary.Throughput = rate(len(samples), duration)
	summary.SuccessRate = percentage(summary.Completed, len(samples))
	summary.RetryRate = percentage(summary.Retried, len(samples))

	return summary
}

func BuildOverall(samples []RunSample, duration time.Duration) ExecutionSummary {
	return SummarizeExecution(samples, duration)
}

func RenderMarkdown(result Result) string {
	var builder strings.Builder

	builder.WriteString("# Benchmark Results\n\n")
	builder.WriteString("## Dataset\n\n")
	builder.WriteString(fmt.Sprintf("- tenants: %d\n", result.Dataset.Tenants))
	builder.WriteString(fmt.Sprintf("- documents: %d (%d per tenant)\n", result.Dataset.Documents, result.Dataset.DocumentsPerTenant))
	builder.WriteString(fmt.Sprintf("- average document size: %d words\n", result.Dataset.DocumentWords))
	builder.WriteString(fmt.Sprintf("- sync runs: %d\n", result.Dataset.SyncRuns))
	builder.WriteString(fmt.Sprintf("- async runs: %d\n", result.Dataset.AsyncRuns))
	builder.WriteString(fmt.Sprintf("- concurrency: %d\n", result.Dataset.Concurrency))
	builder.WriteString(fmt.Sprintf("- async retry bucket: %d%% transient, %d%% dead-letter\n\n", result.Dataset.RetryPercent, result.Dataset.DeadLetterPercent))

	builder.WriteString("## Ingestion\n\n")
	builder.WriteString(fmt.Sprintf("- documents processed: %d\n", result.Ingestion.Documents))
	builder.WriteString(fmt.Sprintf("- total payload: %.2f MB\n", float64(result.Ingestion.Bytes)/(1024*1024)))
	builder.WriteString(fmt.Sprintf("- batch time: %s\n", humanDuration(result.Ingestion.Duration)))
	builder.WriteString(fmt.Sprintf("- ingestion throughput: %.2f docs/sec\n\n", result.Ingestion.Throughput))

	builder.WriteString("## Execution\n\n")
	builder.WriteString(renderExecutionSection("Sync", result.Sync))
	builder.WriteString("\n")
	builder.WriteString(renderExecutionSection("Async", result.Async))
	builder.WriteString("\n")
	builder.WriteString(renderExecutionSection("Overall", result.Overall))

	return builder.String()
}

func renderExecutionSection(name string, summary ExecutionSummary) string {
	return fmt.Sprintf(
		"### %s\n\n- runs: %d\n- completed: %d\n- dead-lettered: %d\n- retried: %d\n- wall time: %s\n- throughput: %.2f runs/sec\n- success rate: %.2f%%\n- retry rate: %.2f%%\n- latency p50: %s\n- latency p95: %s\n",
		name,
		summary.Runs,
		summary.Completed,
		summary.DeadLetters,
		summary.Retried,
		humanDuration(summary.Duration),
		summary.Throughput,
		summary.SuccessRate,
		summary.RetryRate,
		humanDuration(summary.Latency.P50),
		humanDuration(summary.Latency.P95),
	)
}

func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	index := int(math.Ceil((p / 100) * float64(len(durations))))
	if index <= 0 {
		return durations[0]
	}
	if index > len(durations) {
		return durations[len(durations)-1]
	}

	return durations[index-1]
}

func rate(count int, duration time.Duration) float64 {
	if count == 0 || duration <= 0 {
		return 0
	}

	return float64(count) / duration.Seconds()
}

func percentage(count, total int) float64 {
	if total == 0 {
		return 0
	}

	return (float64(count) / float64(total)) * 100
}

func humanDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	if duration >= time.Second {
		return duration.Round(time.Millisecond).String()
	}
	if duration >= time.Millisecond {
		return duration.Round(time.Microsecond).String()
	}

	return duration.String()
}
