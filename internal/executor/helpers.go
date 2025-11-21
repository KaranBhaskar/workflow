package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

func lexicalScore(query, content string) int {
	if query == "" {
		return 1
	}

	score := 0
	lowerContent := strings.ToLower(content)
	for _, term := range strings.Fields(query) {
		if strings.Contains(lowerContent, term) {
			score++
		}
	}

	return score
}

func stringSliceConfig(config map[string]any, key string) []string {
	values, ok := config[key]
	if !ok {
		return nil
	}

	switch typed := values.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(anyString(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringConfig(config map[string]any, key, fallback string) string {
	if value, ok := config[key]; ok {
		if text := strings.TrimSpace(anyString(value)); text != "" {
			return text
		}
	}

	return fallback
}

func stringMapConfig(config map[string]any, key string) map[string]string {
	value, ok := config[key]
	if !ok {
		return nil
	}

	switch typed := value.(type) {
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, value := range typed {
			if cleanKey := strings.TrimSpace(key); cleanKey != "" {
				cloned[cleanKey] = strings.TrimSpace(value)
			}
		}
		return cloned
	case map[string]any:
		cloned := make(map[string]string, len(typed))
		for key, value := range typed {
			cleanKey := strings.TrimSpace(key)
			cleanValue := strings.TrimSpace(anyString(value))
			if cleanKey != "" && cleanValue != "" {
				cloned[cleanKey] = cleanValue
			}
		}
		return cloned
	default:
		return nil
	}
}

func intConfig(config map[string]any, key string, fallback int) int {
	value, ok := config[key]
	if !ok {
		return fallback
	}

	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return renderAny(value)
	}
}

func renderAny(value any) string {
	if value == nil {
		return ""
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	return string(encoded)
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}
