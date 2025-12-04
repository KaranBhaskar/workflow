package executor

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"workflow/internal/workflow"
)

func startTrace(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return otel.Tracer("workflow/internal/executor").Start(ctx, name, oteltrace.WithAttributes(attrs...))
}

func recordTraceError(span oteltrace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func runTraceAttributes(run Run) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tenant.id", run.TenantID),
		attribute.String("workflow.id", run.WorkflowID),
		attribute.String("run.id", run.ID),
		attribute.String("run.trigger_mode", run.TriggerMode),
		attribute.Int("run.attempt", run.Attempt),
		attribute.Int("run.max_attempts", run.MaxAttempts),
	}
}

func jobTraceAttributes(job AsyncJob) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tenant.id", job.TenantID),
		attribute.String("workflow.id", job.WorkflowID),
		attribute.String("run.id", job.RunID),
		attribute.Int("run.attempt", job.Attempt),
		attribute.Int("run.max_attempts", job.MaxAttempts),
	}
}

func nodeTraceAttributes(runID, tenantID string, node workflow.Node) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tenant.id", tenantID),
		attribute.String("run.id", runID),
		attribute.String("node.id", node.ID),
		attribute.String("node.type", node.Type),
	}
}
