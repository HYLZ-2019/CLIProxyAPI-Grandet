package configaccess

import (
	"context"
	"net/http"
	"strings"

	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

// Register ensures the config-access provider is available to the access manager.
func Register(cfg *sdkconfig.SDKConfig) {
	if cfg == nil {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
		return
	}

	keys := normalizeKeyEntries(cfg.APIKeys)
	if len(keys) == 0 {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
		return
	}

	sdkaccess.RegisterProvider(
		sdkaccess.AccessProviderTypeConfigAPIKey,
		newProvider(sdkaccess.DefaultAccessProviderName, keys),
	)
}

type keyEntry struct {
	ID     string
	Name   string
	APIKey string
}

type provider struct {
	name string
	keys map[string]keyEntry
}

func newProvider(name string, keys []keyEntry) *provider {
	providerName := strings.TrimSpace(name)
	if providerName == "" {
		providerName = sdkaccess.DefaultAccessProviderName
	}
	keySet := make(map[string]keyEntry, len(keys))
	for _, key := range keys {
		keySet[key.APIKey] = key
	}
	return &provider{name: providerName, keys: keySet}
}

func (p *provider) Identifier() string {
	if p == nil || p.name == "" {
		return sdkaccess.DefaultAccessProviderName
	}
	return p.name
}

func (p *provider) Authenticate(_ context.Context, r *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	if p == nil {
		return nil, sdkaccess.NewNotHandledError()
	}
	if len(p.keys) == 0 {
		return nil, sdkaccess.NewNotHandledError()
	}
	authHeader := r.Header.Get("Authorization")
	authHeaderGoogle := r.Header.Get("X-Goog-Api-Key")
	authHeaderAnthropic := r.Header.Get("X-Api-Key")
	queryKey := ""
	queryAuthToken := ""
	if r.URL != nil {
		queryKey = r.URL.Query().Get("key")
		queryAuthToken = r.URL.Query().Get("auth_token")
	}
	if authHeader == "" && authHeaderGoogle == "" && authHeaderAnthropic == "" && queryKey == "" && queryAuthToken == "" {
		return nil, sdkaccess.NewNoCredentialsError()
	}

	apiKey := extractBearerToken(authHeader)

	candidates := []struct {
		value  string
		source string
	}{
		{apiKey, "authorization"},
		{authHeaderGoogle, "x-goog-api-key"},
		{authHeaderAnthropic, "x-api-key"},
		{queryKey, "query-key"},
		{queryAuthToken, "query-auth-token"},
	}

	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		entry, ok := p.keys[candidate.value]
		if !ok {
			continue
		}
		metadata := map[string]string{
			"source": candidate.source,
		}
		if entry.ID != "" {
			metadata["api_key_id"] = entry.ID
		}
		if entry.Name != "" {
			metadata["api_key_name"] = entry.Name
		}
		return &sdkaccess.Result{
			Provider:  p.Identifier(),
			Principal: candidate.value,
			Metadata:  metadata,
		}, nil
	}

	return nil, sdkaccess.NewInvalidCredentialError()
}

func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return header
	}
	if strings.ToLower(parts[0]) != "bearer" {
		return header
	}
	return strings.TrimSpace(parts[1])
}

func normalizeKeyEntries(keys []sdkconfig.ClientAPIKey) []keyEntry {
	if len(keys) == 0 {
		return nil
	}
	normalized := sdkconfig.NormalizeClientAPIKeys(keys)
	entries := make([]keyEntry, 0, len(normalized))
	seen := make(map[string]struct{}, len(normalized))
	for _, key := range normalized {
		trimmedKey := strings.TrimSpace(key.APIKey)
		if trimmedKey == "" {
			continue
		}
		if _, exists := seen[trimmedKey]; exists {
			continue
		}
		seen[trimmedKey] = struct{}{}
		entries = append(entries, keyEntry{
			ID:     strings.TrimSpace(key.ID),
			Name:   strings.TrimSpace(key.Name),
			APIKey: trimmedKey,
		})
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}
