package config

import (
	"fmt"
	"os"
	"strings"
)

type AuthConfig struct {
	BootstrapAPIKeys []BootstrapAPIKey
}

type BootstrapAPIKey struct {
	APIKeyID     string
	Label        string
	TenantID     string
	TenantName   string
	PlaintextKey string
}

func bootstrapAuthConfig() (AuthConfig, error) {
	entries, err := parseBootstrapAPIKeys(os.Getenv("BOOTSTRAP_API_KEYS"))
	if err != nil {
		return AuthConfig{}, err
	}

	return AuthConfig{
		BootstrapAPIKeys: entries,
	}, nil
}

func parseBootstrapAPIKeys(raw string) ([]BootstrapAPIKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	items := strings.Split(raw, ",")
	keys := make([]BootstrapAPIKey, 0, len(items))
	for index, item := range items {
		parts := strings.Split(strings.TrimSpace(item), "|")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid BOOTSTRAP_API_KEYS entry %q: expected tenant_id|tenant_name|api_key", item)
		}

		tenantID := strings.TrimSpace(parts[0])
		tenantName := strings.TrimSpace(parts[1])
		plaintext := strings.TrimSpace(parts[2])
		if tenantID == "" || tenantName == "" || plaintext == "" {
			return nil, fmt.Errorf("invalid BOOTSTRAP_API_KEYS entry %q: all fields must be non-empty", item)
		}

		keys = append(keys, BootstrapAPIKey{
			APIKeyID:     fmt.Sprintf("bootstrap-key-%d", index+1),
			Label:        "bootstrap",
			TenantID:     tenantID,
			TenantName:   tenantName,
			PlaintextKey: plaintext,
		})
	}

	return keys, nil
}
