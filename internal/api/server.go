package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"workflow/internal/auth"
	"workflow/internal/documents"
	"workflow/internal/executor"
	"workflow/internal/platform/health"
	"workflow/internal/workflow"
)

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(statusCode int) {
	sr.statusCode = statusCode
	sr.ResponseWriter.WriteHeader(statusCode)
}

type server struct {
	healthService   *health.Service
	documentService *documents.Service
	workflowService *workflow.Service
	executorService *executor.Service
}

type documentEnvelope struct {
	Document documents.Document `json:"document"`
}

type chunksEnvelope struct {
	Chunks []documents.Chunk `json:"chunks"`
}

type workflowEnvelope struct {
	Workflow workflow.Workflow `json:"workflow"`
}

type validationEnvelope struct {
	Validation workflow.ValidationResult `json:"validation"`
}

type runEnvelope struct {
	Run executor.Run `json:"run"`
}

type runsEnvelope struct {
	Runs []executor.Run `json:"runs"`
}

type stepsEnvelope struct {
	Steps []executor.Step `json:"steps"`
}

func NewHandler(logger *slog.Logger, healthService *health.Service, authenticator *auth.Service, documentService *documents.Service, workflowService *workflow.Service, executorService *executor.Service) http.Handler {
	srv := server{
		healthService:   healthService,
		documentService: documentService,
		workflowService: workflowService,
		executorService: executorService,
	}
	mux := http.NewServeMux()

	mux.Handle("GET /health/live", http.HandlerFunc(srv.handleLive))
	mux.Handle("GET /health/ready", http.HandlerFunc(srv.handleReady))
	mux.Handle("GET /v1/tenants/me", requireAPIKey(authenticator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := currentIdentity(w, r)
		if !ok {
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"tenant": map[string]string{
				"id":     identity.TenantID,
				"name":   identity.TenantName,
				"status": string(identity.TenantStatus),
			},
			"api_key": map[string]string{
				"id":    identity.APIKeyID,
				"label": identity.APIKeyLabel,
			},
		})
	})))
	mux.Handle("POST /v1/documents", requireAPIKey(authenticator, http.HandlerFunc(srv.handleUploadDocument)))
	mux.Handle("GET /v1/documents/{documentID}", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetDocument)))
	mux.Handle("GET /v1/documents/{documentID}/chunks", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetDocumentChunks)))
	mux.Handle("POST /v1/workflows", requireAPIKey(authenticator, http.HandlerFunc(srv.handleCreateWorkflow)))
	mux.Handle("GET /v1/workflows/{workflowID}", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetWorkflow)))
	mux.Handle("POST /v1/workflows/{workflowID}/validate", requireAPIKey(authenticator, http.HandlerFunc(srv.handleValidateWorkflow)))
	mux.Handle("POST /v1/workflows/{workflowID}/execute", requireAPIKey(authenticator, http.HandlerFunc(srv.handleExecuteWorkflow)))
	mux.Handle("GET /v1/workflow-runs", requireAPIKey(authenticator, http.HandlerFunc(srv.handleListRuns)))
	mux.Handle("GET /v1/workflow-runs/{runID}", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetRun)))
	mux.Handle("GET /v1/workflow-runs/{runID}/steps", requireAPIKey(authenticator, http.HandlerFunc(srv.handleGetRunSteps)))
	mux.Handle("POST /v1/workflow-runs/{runID}/resume", requireAPIKey(authenticator, http.HandlerFunc(srv.handleResumeRun)))

	return requestLogger(logger, mux)
}

func (s server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.healthService.LiveReport())
}

func (s server) handleReady(w http.ResponseWriter, r *http.Request) {
	report, ready := s.healthService.ReadyReport(r.Context())
	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(w, statusCode, report)
}

func (s server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := serviceContext(w, r, s.documentService, "document")
	if !ok {
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing multipart file field")
		return
	}
	defer file.Close()

	contentReader, contentType, err := sniffedContent(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file")
		return
	}

	document, err := documentService.Create(r.Context(), identity.TenantID, documents.CreateParams{
		Filename:    header.Filename,
		ContentType: contentType,
		Content:     contentReader,
	})
	if err != nil {
		writeDocumentError(w, err, "failed to upload document")
		return
	}

	writeJSON(w, http.StatusCreated, documentEnvelope{Document: document})
}

func (s server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := serviceContext(w, r, s.documentService, "document")
	if !ok {
		return
	}

	document, err := documentService.Get(r.Context(), identity.TenantID, r.PathValue("documentID"))
	if err != nil {
		writeDocumentError(w, err, "failed to fetch document")
		return
	}

	writeJSON(w, http.StatusOK, documentEnvelope{Document: document})
}

func (s server) handleGetDocumentChunks(w http.ResponseWriter, r *http.Request) {
	identity, documentService, ok := serviceContext(w, r, s.documentService, "document")
	if !ok {
		return
	}

	chunks, err := documentService.ListChunks(r.Context(), identity.TenantID, r.PathValue("documentID"))
	if err != nil {
		writeDocumentError(w, err, "failed to fetch document chunks")
		return
	}

	writeJSON(w, http.StatusOK, chunksEnvelope{Chunks: chunks})
}

func (s server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	identity, workflowService, ok := serviceContext(w, r, s.workflowService, "workflow")
	if !ok {
		return
	}

	var definition workflow.Definition
	if err := json.NewDecoder(r.Body).Decode(&definition); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workflow definition payload")
		return
	}

	createdWorkflow, validation, err := workflowService.Create(r.Context(), identity.TenantID, definition)
	if err != nil {
		if errors.Is(err, workflow.ErrInvalidDefinition) {
			writeJSON(w, http.StatusBadRequest, validationEnvelope{Validation: validation})
			return
		}

		writeWorkflowError(w, err, "failed to create workflow")
		return
	}

	writeJSON(w, http.StatusCreated, workflowEnvelope{Workflow: createdWorkflow})
}

func (s server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	identity, workflowService, ok := serviceContext(w, r, s.workflowService, "workflow")
	if !ok {
		return
	}

	resolvedWorkflow, err := workflowService.Get(r.Context(), identity.TenantID, r.PathValue("workflowID"))
	if err != nil {
		writeWorkflowError(w, err, "failed to fetch workflow")
		return
	}

	writeJSON(w, http.StatusOK, workflowEnvelope{Workflow: resolvedWorkflow})
}

func (s server) handleValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	identity, workflowService, ok := serviceContext(w, r, s.workflowService, "workflow")
	if !ok {
		return
	}

	validation, err := workflowService.Validate(r.Context(), identity.TenantID, r.PathValue("workflowID"))
	if err != nil {
		writeWorkflowError(w, err, "failed to validate workflow")
		return
	}

	writeJSON(w, http.StatusOK, validationEnvelope{Validation: validation})
}

func (s server) handleExecuteWorkflow(w http.ResponseWriter, r *http.Request) {
	identity, executorService, ok := serviceContext(w, r, s.executorService, "executor")
	if !ok {
		return
	}

	var request struct {
		Mode    string         `json:"mode"`
		Input   map[string]any `json:"input"`
		Options struct {
			TimeoutMS int `json:"timeout_ms"`
		} `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid workflow execution payload")
		return
	}

	mode := strings.TrimSpace(request.Mode)
	if mode == "" {
		mode = "sync"
	}
	var (
		run        executor.Run
		err        error
		statusCode = http.StatusOK
	)
	switch mode {
	case "sync":
		ctx := r.Context()
		if request.Options.TimeoutMS > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(request.Options.TimeoutMS)*time.Millisecond)
			defer cancel()
		}

		run, err = executorService.ExecuteSync(ctx, identity.TenantID, r.PathValue("workflowID"), request.Input)
	case "async":
		run, err = executorService.ExecuteAsync(r.Context(), identity.TenantID, r.PathValue("workflowID"), request.Input)
		statusCode = http.StatusAccepted
	default:
		writeError(w, http.StatusBadRequest, "execution mode must be sync or async")
		return
	}
	if err != nil {
		writeExecutionError(w, err, "failed to execute workflow")
		return
	}

	writeJSON(w, statusCode, runEnvelope{Run: run})
}

func (s server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	identity, executorService, ok := serviceContext(w, r, s.executorService, "executor")
	if !ok {
		return
	}

	run, err := executorService.GetRun(r.Context(), identity.TenantID, r.PathValue("runID"))
	if err != nil {
		writeExecutionError(w, err, "failed to fetch workflow run")
		return
	}

	writeJSON(w, http.StatusOK, runEnvelope{Run: run})
}

func (s server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	identity, executorService, ok := serviceContext(w, r, s.executorService, "executor")
	if !ok {
		return
	}

	runs, err := executorService.ListRuns(r.Context(), identity.TenantID, executor.RunStatus(strings.TrimSpace(r.URL.Query().Get("status"))))
	if err != nil {
		writeExecutionError(w, err, "failed to list workflow runs")
		return
	}

	writeJSON(w, http.StatusOK, runsEnvelope{Runs: runs})
}

func (s server) handleGetRunSteps(w http.ResponseWriter, r *http.Request) {
	identity, executorService, ok := serviceContext(w, r, s.executorService, "executor")
	if !ok {
		return
	}

	steps, err := executorService.ListSteps(r.Context(), identity.TenantID, r.PathValue("runID"))
	if err != nil {
		writeExecutionError(w, err, "failed to fetch workflow steps")
		return
	}

	writeJSON(w, http.StatusOK, stepsEnvelope{Steps: steps})
}

func (s server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	identity, executorService, ok := serviceContext(w, r, s.executorService, "executor")
	if !ok {
		return
	}

	var request struct {
		Approved *bool          `json:"approved"`
		Comment  string         `json:"comment"`
		Input    map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workflow resume payload")
		return
	}
	if request.Approved == nil {
		writeError(w, http.StatusBadRequest, "approved decision is required")
		return
	}

	run, err := executorService.ResumeApproval(r.Context(), identity.TenantID, r.PathValue("runID"), *request.Approved, request.Comment, request.Input)
	if err != nil {
		writeExecutionError(w, err, "failed to resume workflow run")
		return
	}

	writeJSON(w, http.StatusOK, runEnvelope{Run: run})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(recorder, r)

		logger.Info(
			"http request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status_code", recorder.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{
		"error": message,
	})
}

func currentIdentity(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "request identity missing from context")
		return auth.Identity{}, false
	}

	return identity, true
}

func serviceContext[T any](w http.ResponseWriter, r *http.Request, service *T, name string) (auth.Identity, *T, bool) {
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, name+" service is not configured")
		return auth.Identity{}, nil, false
	}

	identity, ok := currentIdentity(w, r)
	if !ok {
		return auth.Identity{}, nil, false
	}

	return identity, service, true
}

func writeDocumentError(w http.ResponseWriter, err error, fallback string) {
	statusCode := http.StatusInternalServerError
	message := fallback

	switch {
	case errors.Is(err, documents.ErrInvalidFilename), errors.Is(err, documents.ErrMissingContent), errors.Is(err, documents.ErrUnsupportedContentType):
		statusCode = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, documents.ErrNotFound):
		statusCode = http.StatusNotFound
		message = "document not found"
	}

	writeError(w, statusCode, message)
}

func writeWorkflowError(w http.ResponseWriter, err error, fallback string) {
	statusCode := http.StatusInternalServerError
	message := fallback

	switch {
	case errors.Is(err, workflow.ErrNotFound):
		statusCode = http.StatusNotFound
		message = "workflow not found"
	case errors.Is(err, workflow.ErrInvalidTenant), errors.Is(err, workflow.ErrInvalidDefinition):
		statusCode = http.StatusBadRequest
		message = err.Error()
	}

	writeError(w, statusCode, message)
}

func writeExecutionError(w http.ResponseWriter, err error, fallback string) {
	statusCode := http.StatusInternalServerError
	message := fallback

	switch {
	case errors.Is(err, workflow.ErrNotFound):
		statusCode = http.StatusNotFound
		message = "workflow not found"
	case errors.Is(err, executor.ErrNotFound):
		statusCode = http.StatusNotFound
		message = "workflow run not found"
	case errors.Is(err, executor.ErrRunNotAwaitingReview):
		statusCode = http.StatusConflict
		message = err.Error()
	case errors.Is(err, executor.ErrInvalidTenant), errors.Is(err, executor.ErrAsyncDisabled):
		statusCode = http.StatusBadRequest
		message = err.Error()
	}

	writeError(w, statusCode, message)
}

func sniffedContent(file io.Reader) (io.Reader, string, error) {
	head := make([]byte, 512)
	readBytes, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "", err
	}

	contentType := http.DetectContentType(head[:readBytes])
	return io.MultiReader(bytes.NewReader(head[:readBytes]), file), contentType, nil
}

func requireAPIKey(authenticator *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticator == nil {
			writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
			return
		}

		apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
		identity, err := authenticator.Authenticate(r.Context(), apiKey)
		if err != nil {
			statusCode := http.StatusUnauthorized
			message := "invalid api key"

			switch {
			case errors.Is(err, auth.ErrMissingAPIKey):
				message = "missing api key"
			case errors.Is(err, auth.ErrInactiveAPIKey), errors.Is(err, auth.ErrInactiveTenant):
				message = "inactive credential"
			}

			writeError(w, statusCode, message)
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), identity)))
	})
}
