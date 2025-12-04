package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"workflow/internal/documents"
	"workflow/internal/platform/egress"
)

func (s *Service) executeRetrieval(ctx context.Context, tenantID string, config map[string]any, workflowInput map[string]any) (any, error) {
	documentIDs := stringSliceConfig(config, "document_ids")
	if len(documentIDs) == 0 {
		return nil, errors.New("retrieve_documents requires document_ids")
	}

	query := strings.ToLower(strings.TrimSpace(anyString(workflowInput[stringConfig(config, "query_input_key", "query")])))
	limit := intConfig(config, "limit", 3)

	type scoredChunk struct {
		chunk documents.Chunk
		score int
	}

	results := make([]scoredChunk, 0)
	for _, documentID := range documentIDs {
		chunks, err := s.documents.ListChunks(ctx, tenantID, documentID)
		if err != nil {
			return nil, err
		}

		for _, chunk := range chunks {
			score := lexicalScore(query, chunk.Content)
			if query != "" && score == 0 {
				continue
			}
			results = append(results, scoredChunk{chunk: chunk, score: score})
		}
	}

	slices.SortFunc(results, func(left, right scoredChunk) int {
		switch {
		case left.score != right.score:
			return right.score - left.score
		case left.chunk.DocumentID != right.chunk.DocumentID:
			return strings.Compare(left.chunk.DocumentID, right.chunk.DocumentID)
		default:
			return left.chunk.ChunkIndex - right.chunk.ChunkIndex
		}
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	matches := make([]documents.Chunk, 0, len(results))
	for _, result := range results {
		matches = append(matches, result.chunk)
	}

	return map[string]any{
		"query":     query,
		"documents": matches,
	}, nil
}

func (s *Service) executeLLM(ctx context.Context, config map[string]any, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	parts := make([]string, 0, 3)
	if prompt := stringConfig(config, "prompt", ""); prompt != "" {
		parts = append(parts, prompt)
	}
	if inputKey := stringConfig(config, "input_key", ""); inputKey != "" {
		if inputValue := anyString(workflowInput[inputKey]); inputValue != "" {
			parts = append(parts, inputValue)
		}
	}
	if contextStep := stringConfig(config, "context_step", ""); contextStep != "" {
		if contextValue, ok := stepOutputs[contextStep]; ok {
			parts = append(parts, renderAny(contextValue))
		}
	}

	prompt := strings.Join(parts, "\n\n")
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"prompt": prompt,
		"text":   response,
	}, nil
}

func (s *Service) executeHTTPTool(ctx context.Context, config map[string]any, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	url := stringConfig(config, "url", "")
	if err := egress.ValidateHTTPToolURL(url); err != nil {
		return nil, err
	}

	method := strings.ToUpper(stringConfig(config, "method", http.MethodPost))
	payload := httpToolPayload(config, workflowInput, stepOutputs)

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal http tool payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build http tool request: %w", err)
	}
	for key, value := range stringMapConfig(config, "headers") {
		req.Header.Set(key, value)
	}
	if payload != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute http tool request: %w", err)
	}
	defer resp.Body.Close()

	result, err := readHTTPToolResponse(resp, method, url)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func executeAudit(config map[string]any, stepOutputs map[string]any) any {
	result := map[string]any{
		"message": stringConfig(config, "message", "audit logged"),
	}
	if fromStep := stringConfig(config, "from_step", ""); fromStep != "" {
		result["from_step"] = fromStep
		result["value"] = stepOutputs[fromStep]
	}

	return result
}

func executeCondition(config map[string]any, workflowInput map[string]any, stepOutputs map[string]any) (any, error) {
	value := anyString(workflowInput[stringConfig(config, "input_key", "")])
	if contextStep := stringConfig(config, "context_step", ""); contextStep != "" {
		value = renderAny(stepOutputs[contextStep])
	}

	operator := strings.ToLower(stringConfig(config, "operator", "equals"))
	target := stringConfig(config, "equals", stringConfig(config, "value", ""))

	var matched bool
	switch operator {
	case "equals":
		matched = strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target))
	case "contains":
		matched = strings.Contains(strings.ToLower(value), strings.ToLower(target))
	default:
		return nil, fmt.Errorf("unsupported condition operator %q", operator)
	}

	return map[string]any{
		"matched":  matched,
		"operator": operator,
		"value":    value,
		"target":   target,
	}, nil
}

func httpToolPayload(config map[string]any, workflowInput map[string]any, stepOutputs map[string]any) any {
	if body, ok := config["body"]; ok {
		return body
	}

	payload := map[string]any{}
	if inputKey := stringConfig(config, "input_key", ""); inputKey != "" {
		payload["input"] = workflowInput[inputKey]
		payload["input_key"] = inputKey
	}
	if contextStep := stringConfig(config, "context_step", ""); contextStep != "" {
		payload["context"] = stepOutputs[contextStep]
		payload["context_step"] = contextStep
	}
	if len(payload) == 0 {
		return nil
	}

	return payload
}

func readHTTPToolResponse(resp *http.Response, method, url string) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read http tool response: %w", err)
	}

	result := map[string]any{
		"method":      method,
		"url":         url,
		"status_code": resp.StatusCode,
	}
	if len(body) > 0 {
		result["body"] = decodeHTTPToolBody(resp.Header.Get("Content-Type"), body)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("http tool returned status %d", resp.StatusCode)
	}

	return result, nil
}

func decodeHTTPToolBody(contentType string, body []byte) any {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		var decoded any
		if err := json.Unmarshal(body, &decoded); err == nil {
			return decoded
		}
	}

	return text
}
