// Package config provides configuration management for the CLI Proxy API server.
// It handles loading and parsing YAML configuration files, and provides structured
// access to application settings including server port, authentication directory,
// debug settings, proxy configuration, and API keys.
package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClientAPIKey describes a client credential accepted by this proxy server.
type ClientAPIKey struct {
	ID     string `yaml:"id" json:"id"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	APIKey string `yaml:"api-key" json:"api-key"`
}

// UnmarshalYAML accepts both the legacy scalar form and the structured form.
func (k *ClientAPIKey) UnmarshalYAML(value *yaml.Node) error {
	if k == nil || value == nil {
		return nil
	}
	if value.Kind == yaml.ScalarNode {
		k.ID = ""
		k.Name = ""
		k.APIKey = strings.TrimSpace(value.Value)
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("api-key entry must be a string or object")
	}
	var entry struct {
		ID     any    `yaml:"id"`
		Name   string `yaml:"name"`
		APIKey string `yaml:"api-key"`
		Key    string `yaml:"key"`
	}
	if err := value.Decode(&entry); err != nil {
		return err
	}
	k.ID = normalizeClientAPIKeyIDValue(entry.ID)
	k.Name = strings.TrimSpace(entry.Name)
	k.APIKey = strings.TrimSpace(entry.APIKey)
	if k.APIKey == "" {
		k.APIKey = strings.TrimSpace(entry.Key)
	}
	return nil
}

// UnmarshalJSON accepts both the legacy string form and the structured form.
func (k *ClientAPIKey) UnmarshalJSON(data []byte) error {
	if k == nil {
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		k.ID = ""
		k.Name = ""
		k.APIKey = strings.TrimSpace(raw)
		return nil
	}
	var entry struct {
		ID     any    `json:"id"`
		Name   string `json:"name"`
		APIKey string `json:"api-key"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	k.ID = normalizeClientAPIKeyIDValue(entry.ID)
	k.Name = strings.TrimSpace(entry.Name)
	k.APIKey = strings.TrimSpace(entry.APIKey)
	if k.APIKey == "" {
		k.APIKey = strings.TrimSpace(entry.Key)
	}
	return nil
}

func normalizeClientAPIKeyIDValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
	case json.Number:
		return strings.TrimSpace(v.String())
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// NormalizeClientAPIKeys trims entries, drops empty keys, and assigns stable missing IDs.
func NormalizeClientAPIKeys(entries []ClientAPIKey) []ClientAPIKey {
	result, _ := NormalizeClientAPIKeysWithHint(entries, 0)
	return result
}

// NormalizeClientAPIKeysWithHint is like NormalizeClientAPIKeys but treats nextIDHint as the
// minimum value for the next unassigned ID. This prevents ID reuse after deletion: pass
// cfg.APIKeysNextID as the hint so deleted IDs are never reassigned to new keys.
//
// It returns the normalized entries and the updated high-water mark (one past the highest ID
// ever assigned). Callers should store the returned hint back into cfg.APIKeysNextID.
func NormalizeClientAPIKeysWithHint(entries []ClientAPIKey, nextIDHint int) ([]ClientAPIKey, int) {
	if len(entries) == 0 {
		return nil, nextIDHint
	}
	out := make([]ClientAPIKey, 0, len(entries))
	used := make(map[int]struct{}, len(entries))
	pending := make([]int, 0)
	maxID := 0

	for _, entry := range entries {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Name = strings.TrimSpace(entry.Name)
		entry.APIKey = strings.TrimSpace(entry.APIKey)
		if entry.APIKey == "" {
			continue
		}

		idx := len(out)
		id, ok := parsePositiveClientAPIKeyID(entry.ID)
		if ok {
			if _, exists := used[id]; !exists {
				used[id] = struct{}{}
				if id > maxID {
					maxID = id
				}
				entry.ID = strconv.Itoa(id)
				out = append(out, entry)
				continue
			}
		}
		entry.ID = ""
		out = append(out, entry)
		pending = append(pending, idx)
	}

	nextID := maxID + 1
	if nextIDHint > nextID {
		nextID = nextIDHint
	}
	for _, idx := range pending {
		for {
			if _, exists := used[nextID]; !exists {
				break
			}
			nextID++
		}
		out[idx].ID = strconv.Itoa(nextID)
		used[nextID] = struct{}{}
		nextID++
	}
	if len(out) == 0 {
		return nil, nextIDHint
	}
	// newHint is the highest assigned ID + 1, and at least nextIDHint.
	newHint := nextID
	if newHint < nextIDHint {
		newHint = nextIDHint
	}
	return out, newHint
}

// ClientAPIKeyValues returns the raw client secrets from structured entries.
func ClientAPIKeyValues(entries []ClientAPIKey) []string {
	if len(entries) == 0 {
		return nil
	}
	values := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range NormalizeClientAPIKeys(entries) {
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, key)
	}
	return values
}

// NextClientAPIKeyID returns the next stable numeric ID for a new entry.
func NextClientAPIKeyID(entries []ClientAPIKey) string {
	maxID := 0
	for _, entry := range entries {
		if id, ok := parsePositiveClientAPIKeyID(entry.ID); ok && id > maxID {
			maxID = id
		}
	}
	return strconv.Itoa(maxID + 1)
}

// FindClientAPIKeyByID returns the index for a structured client API key ID.
func FindClientAPIKeyByID(entries []ClientAPIKey, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i := range entries {
		if strings.TrimSpace(entries[i].ID) == id {
			return i
		}
	}
	return -1
}

// FindClientAPIKeyByValue returns the matching index and total match count for a raw key.
func FindClientAPIKeyByValue(entries []ClientAPIKey, value string) (int, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return -1, 0
	}
	idx := -1
	count := 0
	for i := range entries {
		if strings.TrimSpace(entries[i].APIKey) == value {
			count++
			if idx == -1 {
				idx = i
			}
		}
	}
	return idx, count
}

func parsePositiveClientAPIKeyID(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// SDKConfig represents the application's configuration, loaded from a YAML file.
type SDKConfig struct {
	// ProxyURL is the URL of an optional proxy server to use for outbound requests.
	ProxyURL string `yaml:"proxy-url" json:"proxy-url"`

	// DisableImageGeneration controls whether the built-in image_generation tool is injected/allowed.
	//
	// Supported values:
	//   - false (default): image_generation is enabled everywhere (normal behavior).
	//   - true: image_generation is disabled everywhere. The server stops injecting it, removes it from request payloads,
	//     and returns 404 for /v1/images/generations and /v1/images/edits.
	//   - "chat": disable image_generation injection for all non-images endpoints (e.g. /v1/responses, /v1/chat/completions),
	//     while keeping /v1/images/generations and /v1/images/edits enabled and preserving image_generation there.
	DisableImageGeneration DisableImageGenerationMode `yaml:"disable-image-generation" json:"disable-image-generation"`

	// EnableGeminiCLIEndpoint controls whether Gemini CLI internal endpoints (/v1internal:*) are enabled.
	// Default is false for safety; when false, /v1internal:* requests are rejected.
	EnableGeminiCLIEndpoint bool `yaml:"enable-gemini-cli-endpoint" json:"enable-gemini-cli-endpoint"`

	// ForceModelPrefix requires explicit model prefixes (e.g., "teamA/gemini-3-pro-preview")
	// to target prefixed credentials. When false, unprefixed model requests may use prefixed
	// credentials as well.
	ForceModelPrefix bool `yaml:"force-model-prefix" json:"force-model-prefix"`

	// RequestLog enables or disables detailed request logging functionality.
	RequestLog bool `yaml:"request-log" json:"request-log"`

	// APIKeys is a list of client credentials for authenticating clients to this proxy server.
	APIKeys []ClientAPIKey `yaml:"api-keys" json:"api-keys"`

	// APIKeysNextID is a monotonically increasing counter that tracks the highest numeric ID
	// ever assigned to a client API key. When a new key is added, it receives this value as its
	// ID and the counter is incremented. Deleted key IDs are never reused.
	APIKeysNextID int `yaml:"api-keys-next-id,omitempty" json:"api-keys-next-id,omitempty"`

	// PassthroughHeaders controls whether upstream response headers are forwarded to downstream clients.
	// Default is false (disabled).
	PassthroughHeaders bool `yaml:"passthrough-headers" json:"passthrough-headers"`

	// Streaming configures server-side streaming behavior (keep-alives and safe bootstrap retries).
	Streaming StreamingConfig `yaml:"streaming" json:"streaming"`

	// NonStreamKeepAliveInterval controls how often blank lines are emitted for non-streaming responses.
	// <= 0 disables keep-alives. Value is in seconds.
	NonStreamKeepAliveInterval int `yaml:"nonstream-keepalive-interval,omitempty" json:"nonstream-keepalive-interval,omitempty"`
}

// StreamingConfig holds server streaming behavior configuration.
type StreamingConfig struct {
	// KeepAliveSeconds controls how often the server emits SSE heartbeats (": keep-alive\n\n").
	// <= 0 disables keep-alives. Default is 0.
	KeepAliveSeconds int `yaml:"keepalive-seconds,omitempty" json:"keepalive-seconds,omitempty"`

	// BootstrapRetries controls how many times the server may retry a streaming request before any bytes are sent,
	// to allow auth rotation / transient recovery.
	// <= 0 disables bootstrap retries. Default is 0.
	BootstrapRetries int `yaml:"bootstrap-retries,omitempty" json:"bootstrap-retries,omitempty"`
}
