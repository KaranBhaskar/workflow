package executor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"workflow/internal/documents"
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
