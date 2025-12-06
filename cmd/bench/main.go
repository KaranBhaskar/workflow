package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"workflow/internal/api"
	"workflow/internal/audit"
	"workflow/internal/auth"
	bench "workflow/internal/benchmark"
	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/platform/health"
	"workflow/internal/tenant"
	appworker "workflow/internal/worker"
	"workflow/internal/workflow"
)

const workflowDocumentLimit = 8

type options struct {
	tenants            int
	documentsPerTenant int
	documentWords      int
	syncRuns           int
	asyncRuns          int
	concurrency        int
	retryPercent       int
	deadLetterPercent  int
	outputPath         string
}

type benchTenant struct {
	ID              string
	Name            string
	APIKey          string
	DocumentIDs     []string
	SyncWorkflowID  string
	AsyncWorkflowID string
}

type runState struct {
	Tenant    benchTenant
	RunID     string
	StartedAt time.Time
}

type runEnvelope struct {
	Run struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Attempt int    `json:"attempt"`
	} `json:"run"`
}

type benchmarkLLM struct {
	mu                sync.Mutex
	attempts          map[string]int
	retryPercent      int
	deadLetterPercent int
}

func main() {
	opts := parseFlags()
	if err := validateOptions(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := runBenchmark(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	markdown := bench.RenderMarkdown(result)
	fmt.Print(markdown)

	if strings.TrimSpace(opts.outputPath) == "" {
		return
	}
	if err := os.WriteFile(opts.outputPath, []byte(markdown), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write benchmark output: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.IntVar(&opts.tenants, "tenants", 4, "number of benchmark tenants")
	flag.IntVar(&opts.documentsPerTenant, "docs-per-tenant", 200, "documents uploaded per tenant")
	flag.IntVar(&opts.documentWords, "doc-words", 900, "approximate words per generated document")
	flag.IntVar(&opts.syncRuns, "sync-runs", 1200, "number of sync executions")
	flag.IntVar(&opts.asyncRuns, "async-runs", 800, "number of async executions")
	flag.IntVar(&opts.concurrency, "concurrency", 24, "client-side concurrency")
	flag.IntVar(&opts.retryPercent, "retry-percent", 12, "percent of async runs that fail once before succeeding")
	flag.IntVar(&opts.deadLetterPercent, "dead-letter-percent", 3, "percent of async runs that fail all retry attempts")
	flag.StringVar(&opts.outputPath, "output", "", "optional markdown output path")
	flag.Parse()

	return opts
}

func validateOptions(opts options) error {
	switch {
	case opts.tenants <= 0:
		return errors.New("tenants must be greater than zero")
	case opts.documentsPerTenant <= 0:
		return errors.New("docs-per-tenant must be greater than zero")
	case opts.documentWords <= 0:
		return errors.New("doc-words must be greater than zero")
	case opts.syncRuns < 0:
		return errors.New("sync-runs cannot be negative")
	case opts.asyncRuns < 0:
		return errors.New("async-runs cannot be negative")
	case opts.concurrency <= 0:
		return errors.New("concurrency must be greater than zero")
	case opts.retryPercent < 0 || opts.retryPercent > 100:
		return errors.New("retry-percent must be between 0 and 100")
	case opts.deadLetterPercent < 0 || opts.deadLetterPercent > 100:
		return errors.New("dead-letter-percent must be between 0 and 100")
	case opts.retryPercent+opts.deadLetterPercent > 100:
		return errors.New("retry-percent plus dead-letter-percent must not exceed 100")
	default:
		return nil
	}
}

func runBenchmark(ctx context.Context, opts options) (bench.Result, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tempDir, err := os.MkdirTemp("", "workflow-bench-*")
	if err != nil {
		return bench.Result{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tenants := generateTenants(opts.tenants)
	healthService := health.NewService("workflow-bench", time.Second)
	tenantService := tenant.NewService(tenant.NewMemoryRepository(toTenantModels(tenants)))
	authService := auth.NewService(auth.NewMemoryRepository(toAPIKeys(tenants)), tenantService)
	documentService := documents.NewService(documents.NewMemoryRepository(), documents.NewLocalObjectStore(filepath.Join(tempDir, "objects")))
	triggerLimit := opts.syncRuns + opts.asyncRuns + 100
	triggerControl := tenant.NewTriggerControl(triggerLimit, time.Hour)
	auditService := audit.NewService(audit.NewMemoryRepository())
	workflowService := workflow.NewService(workflow.NewMemoryRepository())
	jobQueue := appworker.NewMemoryQueue(max(128, opts.asyncRuns*2))
	executorService := executor.NewService(
		executor.NewMemoryRepository(),
		workflowService,
		documentService,
		newBenchmarkLLM(opts.retryPercent, opts.deadLetterPercent),
	).WithJobQueue(jobQueue).WithAudit(auditService).WithTriggerControl(triggerControl)
	workerService := appworker.NewService(logger, jobQueue, executorService).WithPollTimeout(2 * time.Millisecond)

	handler := api.NewHandler(logger, healthService, authService, documentService, workflowService, executorService, auditService)
	client := newLocalHTTPClient(handler)
	baseURL := "http://workflow.local"

	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	workerErrCh := make(chan error, 1)
	go func() {
		if err := workerService.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			workerErrCh <- err
		}
	}()

	ingestion, err := uploadDocuments(ctx, client, baseURL, tenants, opts)
	if err != nil {
		return bench.Result{}, err
	}
	if err := createBenchmarkWorkflows(ctx, client, baseURL, tenants); err != nil {
		return bench.Result{}, err
	}

	syncSummary, syncSamples, syncWall, err := runSyncBenchmark(ctx, client, baseURL, tenants, opts)
	if err != nil {
		return bench.Result{}, err
	}

	asyncSummary, asyncSamples, asyncWall, err := runAsyncBenchmark(ctx, client, baseURL, tenants, opts)
	if err != nil {
		return bench.Result{}, err
	}

	select {
	case err := <-workerErrCh:
		return bench.Result{}, fmt.Errorf("worker failed during benchmark: %w", err)
	default:
	}

	result := bench.Result{
		GeneratedAt: time.Now().UTC(),
		Dataset: bench.Dataset{
			Tenants:            opts.tenants,
			Documents:          opts.tenants * opts.documentsPerTenant,
			DocumentsPerTenant: opts.documentsPerTenant,
			DocumentWords:      opts.documentWords,
			SyncRuns:           opts.syncRuns,
			AsyncRuns:          opts.asyncRuns,
			Concurrency:        opts.concurrency,
			RetryPercent:       opts.retryPercent,
			DeadLetterPercent:  opts.deadLetterPercent,
		},
		Ingestion: ingestion,
		Sync:      syncSummary,
		Async:     asyncSummary,
	}
	result.Overall = bench.BuildOverall(append(syncSamples, asyncSamples...), syncWall+asyncWall)

	return result, nil
}

type localRoundTripper struct {
	handler http.Handler
}

func newLocalHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: localRoundTripper{handler: handler},
	}
}

func (rt localRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	rt.handler.ServeHTTP(recorder, req)

	return recorder.Result(), nil
}

func uploadDocuments(ctx context.Context, client *http.Client, baseURL string, tenants []benchTenant, opts options) (bench.IngestionSummary, error) {
	totalDocs := len(tenants) * opts.documentsPerTenant
	results := make(chan uploadResult, totalDocs)
	start := time.Now()

	err := forEachConcurrent(totalDocs, opts.concurrency, func(index int) error {
		tenantIndex := index % len(tenants)
		documentIndex := index / len(tenants)
		payload := generateDocumentPayload(tenantIndex, documentIndex, opts.documentWords)
		documentID, bytesWritten, err := uploadDocument(ctx, client, baseURL, tenants[tenantIndex], documentIndex, payload)
		if err != nil {
			return err
		}

		results <- uploadResult{
			TenantIndex: tenantIndex,
			DocumentID:  documentID,
			Bytes:       bytesWritten,
		}
		return nil
	})
	close(results)
	if err != nil {
		return bench.IngestionSummary{}, err
	}

	var totalBytes int64
	for result := range results {
		tenants[result.TenantIndex].DocumentIDs = append(tenants[result.TenantIndex].DocumentIDs, result.DocumentID)
		totalBytes += result.Bytes
	}

	duration := time.Since(start)
	return bench.IngestionSummary{
		Documents:  totalDocs,
		Bytes:      totalBytes,
		Duration:   duration,
		Throughput: float64(totalDocs) / duration.Seconds(),
	}, nil
}

type uploadResult struct {
	TenantIndex int
	DocumentID  string
	Bytes       int64
}

func createBenchmarkWorkflows(ctx context.Context, client *http.Client, baseURL string, tenants []benchTenant) error {
	for index := range tenants {
		documentIDs := tenants[index].DocumentIDs
		if len(documentIDs) > workflowDocumentLimit {
			documentIDs = documentIDs[:workflowDocumentLimit]
		}

		syncWorkflowID, err := createWorkflow(ctx, client, baseURL, tenants[index], workflow.Definition{
			Name:    "sync-bench",
			Version: 1,
			Nodes: []workflow.Node{
				{ID: "retrieve", Type: "retrieve_documents", Config: map[string]any{"document_ids": documentIDs, "query_input_key": "query", "limit": 5}},
				{ID: "llm", Type: "llm", Config: map[string]any{"prompt": "SYNC BENCH", "input_key": "query", "context_step": "retrieve"}},
				{ID: "audit", Type: "audit_log", Config: map[string]any{"message": "sync-complete", "from_step": "llm"}},
			},
			Edges: []workflow.Edge{
				{From: "retrieve", To: "llm"},
				{From: "llm", To: "audit"},
			},
		})
		if err != nil {
			return err
		}

		asyncWorkflowID, err := createWorkflow(ctx, client, baseURL, tenants[index], workflow.Definition{
			Name:    "async-bench",
			Version: 1,
			Nodes: []workflow.Node{
				{ID: "retrieve", Type: "retrieve_documents", Config: map[string]any{"document_ids": documentIDs, "query_input_key": "query", "limit": 5}},
				{ID: "llm", Type: "llm", Config: map[string]any{"prompt": "ASYNC BENCH", "input_key": "query", "context_step": "retrieve"}},
				{ID: "audit", Type: "audit_log", Config: map[string]any{"message": "async-complete", "from_step": "llm"}},
			},
			Edges: []workflow.Edge{
				{From: "retrieve", To: "llm"},
				{From: "llm", To: "audit"},
			},
		})
		if err != nil {
			return err
		}

		tenants[index].SyncWorkflowID = syncWorkflowID
		tenants[index].AsyncWorkflowID = asyncWorkflowID
	}

	return nil
}

func runSyncBenchmark(ctx context.Context, client *http.Client, baseURL string, tenants []benchTenant, opts options) (bench.ExecutionSummary, []bench.RunSample, time.Duration, error) {
	samples := make([]bench.RunSample, opts.syncRuns)
	start := time.Now()

	err := forEachConcurrent(opts.syncRuns, opts.concurrency, func(index int) error {
		tenantCfg := tenants[index%len(tenants)]
		payload := map[string]any{
			"mode": "sync",
			"input": map[string]any{
				"query": fmt.Sprintf("sync query %d tenant %s", index, tenantCfg.ID),
			},
		}

		requestStarted := time.Now()
		statusCode, body, err := doJSONRequest(ctx, client, http.MethodPost, baseURL+"/v1/workflows/"+tenantCfg.SyncWorkflowID+"/execute", tenantCfg.APIKey, payload)
		if err != nil {
			return err
		}
		defer body.Close()
		if statusCode != http.StatusOK {
			return fmt.Errorf("sync execution status: %d", statusCode)
		}

		var envelope runEnvelope
		if err := json.NewDecoder(body).Decode(&envelope); err != nil {
			return fmt.Errorf("decode sync run: %w", err)
		}

		samples[index] = bench.RunSample{
			Duration:  time.Since(requestStarted),
			Completed: envelope.Run.Status == "completed",
		}
		return nil
	})
	if err != nil {
		return bench.ExecutionSummary{}, nil, 0, err
	}

	duration := time.Since(start)
	return bench.SummarizeExecution(samples, duration), samples, duration, nil
}

func runAsyncBenchmark(ctx context.Context, client *http.Client, baseURL string, tenants []benchTenant, opts options) (bench.ExecutionSummary, []bench.RunSample, time.Duration, error) {
	states := make([]runState, opts.asyncRuns)
	start := time.Now()

	err := forEachConcurrent(opts.asyncRuns, opts.concurrency, func(index int) error {
		tenantCfg := tenants[index%len(tenants)]
		payload := map[string]any{
			"mode": "async",
			"input": map[string]any{
				"query": fmt.Sprintf("async query %d tenant %s", index, tenantCfg.ID),
			},
		}

		startedAt := time.Now()
		statusCode, body, err := doJSONRequest(ctx, client, http.MethodPost, baseURL+"/v1/workflows/"+tenantCfg.AsyncWorkflowID+"/execute", tenantCfg.APIKey, payload)
		if err != nil {
			return err
		}
		defer body.Close()
		if statusCode != http.StatusAccepted {
			return fmt.Errorf("async execution status: %d", statusCode)
		}

		var envelope runEnvelope
		if err := json.NewDecoder(body).Decode(&envelope); err != nil {
			return fmt.Errorf("decode async run: %w", err)
		}

		states[index] = runState{
			Tenant:    tenantCfg,
			RunID:     envelope.Run.ID,
			StartedAt: startedAt,
		}
		return nil
	})
	if err != nil {
		return bench.ExecutionSummary{}, nil, 0, err
	}
	samples := make([]bench.RunSample, len(states))
	outstanding := make(map[string]int, len(states))
	for index, state := range states {
		outstanding[state.RunID] = index
	}

	pollTicker := time.NewTicker(10 * time.Millisecond)
	defer pollTicker.Stop()

	for len(outstanding) > 0 {
		select {
		case <-ctx.Done():
			return bench.ExecutionSummary{}, nil, 0, ctx.Err()
		case <-pollTicker.C:
			for runID, index := range outstanding {
				envelope, err := fetchRun(ctx, client, baseURL, states[index].Tenant.APIKey, runID)
				if err != nil {
					return bench.ExecutionSummary{}, nil, 0, err
				}
				if envelope.Run.Status != "completed" && envelope.Run.Status != "dead_letter" {
					continue
				}

				samples[index] = bench.RunSample{
					Duration:   time.Since(states[index].StartedAt),
					Completed:  envelope.Run.Status == "completed",
					DeadLetter: envelope.Run.Status == "dead_letter",
					Retried:    envelope.Run.Attempt > 1,
				}
				delete(outstanding, runID)
			}
		}
	}

	duration := time.Since(start)
	return bench.SummarizeExecution(samples, duration), samples, duration, nil
}

func fetchRun(ctx context.Context, client *http.Client, baseURL, apiKey, runID string) (runEnvelope, error) {
	statusCode, body, err := doJSONRequest(ctx, client, http.MethodGet, baseURL+"/v1/workflow-runs/"+runID, apiKey, nil)
	if err != nil {
		return runEnvelope{}, err
	}
	defer body.Close()
	if statusCode != http.StatusOK {
		return runEnvelope{}, fmt.Errorf("fetch run %s status: %d", runID, statusCode)
	}

	var envelope runEnvelope
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return runEnvelope{}, fmt.Errorf("decode run %s: %w", runID, err)
	}

	return envelope, nil
}

func uploadDocument(ctx context.Context, client *http.Client, baseURL string, tenantCfg benchTenant, documentIndex int, payload []byte) (string, int64, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fmt.Sprintf("tenant-%s-%04d.txt", tenantCfg.ID, documentIndex))
	if err != nil {
		return "", 0, fmt.Errorf("create multipart upload: %w", err)
	}
	if _, err := part.Write(payload); err != nil {
		return "", 0, fmt.Errorf("write upload payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", 0, fmt.Errorf("close upload payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/documents", body)
	if err != nil {
		return "", 0, fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("X-API-Key", tenantCfg.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("upload document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", 0, fmt.Errorf("upload document status: %d", resp.StatusCode)
	}

	var envelope struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", 0, fmt.Errorf("decode upload response: %w", err)
	}

	return envelope.Document.ID, int64(len(payload)), nil
}

func createWorkflow(ctx context.Context, client *http.Client, baseURL string, tenantCfg benchTenant, definition workflow.Definition) (string, error) {
	statusCode, body, err := doJSONRequest(ctx, client, http.MethodPost, baseURL+"/v1/workflows", tenantCfg.APIKey, definition)
	if err != nil {
		return "", err
	}
	defer body.Close()
	if statusCode != http.StatusCreated {
		return "", fmt.Errorf("create workflow status: %d", statusCode)
	}

	var envelope struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("decode workflow response: %w", err)
	}

	return envelope.Workflow.ID, nil
}

func doJSONRequest(ctx context.Context, client *http.Client, method, url, apiKey string, payload any) (int, io.ReadCloser, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("perform request: %w", err)
	}

	return resp.StatusCode, resp.Body, nil
}

func generateTenants(count int) []benchTenant {
	tenants := make([]benchTenant, 0, count)
	for index := 0; index < count; index++ {
		ordinal := index + 1
		tenants = append(tenants, benchTenant{
			ID:     fmt.Sprintf("tenant-%02d", ordinal),
			Name:   fmt.Sprintf("Tenant %02d", ordinal),
			APIKey: fmt.Sprintf("example-bench-key-%02d", ordinal),
		})
	}

	return tenants
}

func toTenantModels(tenants []benchTenant) []tenant.Tenant {
	models := make([]tenant.Tenant, 0, len(tenants))
	for _, tenantCfg := range tenants {
		models = append(models, tenant.Tenant{
			ID:     tenantCfg.ID,
			Name:   tenantCfg.Name,
			Status: tenant.StatusActive,
		})
	}

	return models
}

func toAPIKeys(tenants []benchTenant) []auth.APIKey {
	keys := make([]auth.APIKey, 0, len(tenants))
	for _, tenantCfg := range tenants {
		keys = append(keys, auth.APIKey{
			ID:        tenantCfg.ID + "-key",
			TenantID:  tenantCfg.ID,
			Label:     "benchmark",
			Plaintext: tenantCfg.APIKey,
			Status:    auth.StatusActive,
		})
	}

	return keys
}

func newBenchmarkLLM(retryPercent, deadLetterPercent int) *benchmarkLLM {
	return &benchmarkLLM{
		attempts:          make(map[string]int),
		retryPercent:      retryPercent,
		deadLetterPercent: deadLetterPercent,
	}
}

func (l *benchmarkLLM) Generate(_ context.Context, prompt string) (string, error) {
	l.mu.Lock()
	l.attempts[prompt]++
	attempt := l.attempts[prompt]
	l.mu.Unlock()

	if strings.Contains(prompt, "ASYNC BENCH") {
		bucket := stableBucket(prompt)
		if bucket < l.deadLetterPercent {
			return "", errors.New("benchmark llm terminal failure")
		}
		if bucket < l.deadLetterPercent+l.retryPercent && attempt == 1 {
			return "", errors.New("benchmark llm transient failure")
		}
	}

	return "benchmark-response", nil
}

func stableBucket(text string) int {
	var hash uint32 = 2166136261
	for _, char := range text {
		hash ^= uint32(char)
		hash *= 16777619
	}

	return int(hash % 100)
}

func generateDocumentPayload(tenantIndex, documentIndex, words int) []byte {
	builder := strings.Builder{}
	builder.Grow(words * 12)

	topic := fmt.Sprintf("tenant-%02d", tenantIndex+1)
	for index := 0; index < words; index++ {
		if index > 0 {
			builder.WriteByte(' ')
		}
		switch index % 8 {
		case 0:
			builder.WriteString("workflow")
		case 1:
			builder.WriteString("orchestration")
		case 2:
			builder.WriteString("backend")
		case 3:
			builder.WriteString("audit")
		case 4:
			builder.WriteString("retrieval")
		case 5:
			builder.WriteString(topic)
		case 6:
			builder.WriteString(fmt.Sprintf("doc-%04d", documentIndex))
		default:
			builder.WriteString("reliability")
		}
	}

	return []byte(builder.String())
}

func forEachConcurrent(total, concurrency int, fn func(index int) error) error {
	if total == 0 {
		return nil
	}

	workers := min(total, concurrency)
	var (
		next     atomic.Int64
		firstErr error
		errMu    sync.Mutex
		wg       sync.WaitGroup
	)

	recordErr := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				errMu.Lock()
				if firstErr != nil {
					errMu.Unlock()
					return
				}
				errMu.Unlock()

				index := int(next.Add(1)) - 1
				if index >= total {
					return
				}
				if err := fn(index); err != nil {
					recordErr(err)
					return
				}
			}
		}()
	}

	wg.Wait()
	return firstErr
}

func min(left, right int) int {
	if left < right {
		return left
	}

	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}

	return right
}
