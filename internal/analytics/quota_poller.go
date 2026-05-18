package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// QuotaPoller periodically queries provider quota APIs using OAuth tokens from the auth manager.
type QuotaPoller struct {
	store   *Store
	manager *coreauth.Manager
	client  *http.Client
}

// StartQuotaPoller creates and starts a QuotaPoller. It polls immediately on start,
// then every hour. Stops when ctx is cancelled.
func StartQuotaPoller(ctx context.Context, store *Store, manager *coreauth.Manager) {
	if store == nil || manager == nil {
		return
	}
	p := &QuotaPoller{
		store:   store,
		manager: manager,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	go p.run(ctx)
}

func (p *QuotaPoller) run(ctx context.Context) {
	p.pollAll(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.pollAll(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (p *QuotaPoller) pollAll(ctx context.Context) {
	for _, auth := range p.manager.List() {
		token, _ := auth.Metadata["access_token"].(string)
		if token == "" {
			continue
		}
		switch auth.Provider {
		case "claude", "antigravity":
			p.pollClaude(ctx, auth.ID, token)
		case "codex":
			p.pollCodex(ctx, auth.ID, token)
		case "gemini-cli":
			p.pollGeminiCLI(ctx, auth.ID, token)
		}
	}
}

// pollClaude calls api.anthropic.com/api/oauth/usage.
func (p *QuotaPoller) pollClaude(ctx context.Context, authID, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Debugf("analytics: claude quota poll failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	// Try to extract usage percentage from various response shapes.
	// Claude's oauth/usage response varies; we store raw used_percent if found.
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	ts := time.Now().Unix()
	p.extractAndStore(ts, "claude", authID, data)
}

// pollCodex calls chatgpt.com/backend-api/wham/usage.
func (p *QuotaPoller) pollCodex(ctx context.Context, authID, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Debugf("analytics: codex quota poll failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	// Codex returns {"windows": [{"window_type": "5h", "used_percent": 42, "reset_at": ...}, ...]}
	var data struct {
		Windows []struct {
			WindowType  string  `json:"window_type"`
			UsedPercent float64 `json:"used_percent"`
			ResetAt     int64   `json:"reset_at"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	ts := time.Now().Unix()
	for _, w := range data.Windows {
		p.store.InsertQuotaSnapshot(ts, "codex", authID, w.WindowType, w.UsedPercent, w.ResetAt)
	}
}

// pollGeminiCLI calls cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota.
func (p *QuotaPoller) pollGeminiCLI(ctx context.Context, authID, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota",
		strings.NewReader("{}"))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Debugf("analytics: gemini-cli quota poll failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	ts := time.Now().Unix()
	p.extractAndStore(ts, "gemini-cli", authID, data)
}

// extractAndStore tries common field patterns to pull a used_percent value.
func (p *QuotaPoller) extractAndStore(ts int64, provider, authID string, data map[string]any) {
	// Try top-level used_percent
	if v, ok := getFloat(data, "used_percent"); ok {
		p.store.InsertQuotaSnapshot(ts, provider, authID, "default", v, 0)
		return
	}
	// Try nested quota objects
	for k, val := range data {
		if sub, ok := val.(map[string]any); ok {
			if v, ok := getFloat(sub, "used_percent"); ok {
				p.store.InsertQuotaSnapshot(ts, provider, authID, k, v, 0)
			}
		}
	}
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// RetentionDays returns the current raw-log retention in days (for display).
func (s *Store) RetentionDays() int {
	if s == nil {
		return defaultRetentionDays
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	days := s.rawRetentionSeconds / 86400
	if days <= 0 {
		return defaultRetentionDays
	}
	return int(days)
}

// Enabled reports whether the analytics store has been initialized.
func Enabled() bool { return Get() != nil }

// UpdateRetention updates both the global store's retention and the config.
func UpdateRetention(days int) error {
	store := Get()
	if store == nil {
		return fmt.Errorf("analytics not initialized")
	}
	store.SetRetention(days)
	return nil
}
