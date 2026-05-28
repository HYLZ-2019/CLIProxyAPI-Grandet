package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// QuotaPoller periodically queries provider quota APIs using OAuth tokens from the auth manager.
type QuotaPoller struct {
	store        *Store
	manager      *coreauth.Manager
	client       *http.Client
	debugLogPath string
}

// quotaPollInterval is how often we re-query upstream provider quota endpoints.
const quotaPollInterval = time.Duration(analyticsFiveMinuteBucketSeconds) * time.Second

// StartQuotaPoller creates and starts a QuotaPoller. It polls on aligned quotaPollInterval boundaries.
func StartQuotaPoller(ctx context.Context, store *Store, manager *coreauth.Manager) {
	if store == nil || manager == nil {
		return
	}
	p := &QuotaPoller{
		store:        store,
		manager:      manager,
		client:       &http.Client{Timeout: 30 * time.Second},
		debugLogPath: filepath.Join(filepath.Dir(store.DBPath()), "quota-response-debug.jsonl"),
	}
	go p.run(ctx)
}

func (p *QuotaPoller) run(ctx context.Context) {
	for {
		nextPoll := nextQuotaPollBoundary(time.Now())
		timer := time.NewTimer(time.Until(nextPoll))
		select {
		case <-timer.C:
			p.pollAll(ctx, nextPoll.Unix())
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func nextQuotaPollBoundary(now time.Time) time.Time {
	intervalSeconds := int64(quotaPollInterval / time.Second)
	nextTS := floorBucket(now.Unix(), intervalSeconds)
	if now.Unix()%intervalSeconds != 0 || now.Nanosecond() != 0 {
		nextTS += intervalSeconds
	}
	return time.Unix(nextTS, 0)
}

func (p *QuotaPoller) pollAll(ctx context.Context, ts int64) {
	for _, auth := range p.manager.List() {
		token, _ := auth.Metadata["access_token"].(string)
		if token == "" {
			continue
		}
		switch auth.Provider {
		case "claude", "antigravity":
			p.pollClaude(ctx, ts, auth.ID, token)
		case "codex":
			p.pollCodex(ctx, ts, auth.ID, token)
		case "gemini-cli":
			p.pollGeminiCLI(ctx, ts, auth.ID, token)
		}
	}
}

// pollClaude calls api.anthropic.com/api/oauth/usage.
func (p *QuotaPoller) pollClaude(ctx context.Context, ts int64, authID, token string) {
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
	body, _ := io.ReadAll(resp.Body)
	p.logQuotaResponse(ts, "claude", authID, req.URL.String(), resp.StatusCode, body)
	if resp.StatusCode != http.StatusOK {
		return
	}

	captureClaudeQuotaSnapshots(p.store, ts, "claude", authID, body)
}

// pollCodex calls chatgpt.com/backend-api/wham/usage.
func (p *QuotaPoller) pollCodex(ctx context.Context, ts int64, authID, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Debugf("analytics: codex quota poll failed: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	p.logQuotaResponse(ts, "codex", authID, req.URL.String(), resp.StatusCode, body)
	if resp.StatusCode != http.StatusOK {
		return
	}

	captureCodexQuotaSnapshots(p.store, ts, "codex", authID, body)
}

// pollGeminiCLI calls cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota.
func (p *QuotaPoller) pollGeminiCLI(ctx context.Context, ts int64, authID, token string) {
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
	body, _ := io.ReadAll(resp.Body)
	p.logQuotaResponse(ts, "gemini-cli", authID, req.URL.String(), resp.StatusCode, body)
	if resp.StatusCode != http.StatusOK {
		return
	}

	captureGeminiCLIQuotaSnapshots(p.store, ts, "gemini-cli", authID, body)
}

func (p *QuotaPoller) logQuotaResponse(ts int64, provider, authID, endpoint string, statusCode int, body []byte) {
	if p.debugLogPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.debugLogPath), 0o700); err != nil {
		log.Debugf("analytics: create quota debug log dir failed: %v", err)
		return
	}
	entry := map[string]any{
		"ts":          ts,
		"time":        time.Unix(ts, 0).Format(time.RFC3339),
		"provider":    provider,
		"auth_id":     authID,
		"endpoint":    endpoint,
		"status_code": statusCode,
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		entry["body"] = parsed
	} else {
		entry["body"] = string(body)
	}
	line, err := json.Marshal(entry)
	if err != nil {
		log.Debugf("analytics: marshal quota debug log failed: %v", err)
		return
	}
	file, err := os.OpenFile(p.debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Debugf("analytics: open quota debug log failed: %v", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		log.Debugf("analytics: write quota debug log failed: %v", err)
	}
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

func CaptureQuotaSnapshotFromAPIResponse(store *Store, ts int64, authProvider, authID, method, rawURL string, body []byte) bool {
	if store == nil || strings.TrimSpace(authID) == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return false
	}

	method = strings.ToUpper(strings.TrimSpace(method))
	host := strings.ToLower(parsed.Hostname())
	path := parsed.EscapedPath()
	provider := strings.ToLower(strings.TrimSpace(authProvider))
	if provider == "" {
		provider = providerFromQuotaEndpoint(method, host, path)
	}

	switch {
	case method == http.MethodGet && host == "api.anthropic.com" && path == "/api/oauth/usage":
		if provider != "antigravity" {
			provider = "claude"
		}
		return captureClaudeQuotaSnapshots(store, ts, provider, authID, body) > 0
	case method == http.MethodGet && host == "chatgpt.com" && path == "/backend-api/wham/usage":
		return captureCodexQuotaSnapshots(store, ts, "codex", authID, body) > 0
	case method == http.MethodPost && host == "cloudcode-pa.googleapis.com" && path == "/v1internal:retrieveUserQuota":
		return captureGeminiCLIQuotaSnapshots(store, ts, "gemini-cli", authID, body) > 0
	case method == http.MethodPost && isAntigravityQuotaHost(host) && path == "/v1internal:fetchAvailableModels":
		return captureAntigravityQuotaSnapshots(store, ts, "antigravity", authID, body) > 0
	default:
		return false
	}
}

func providerFromQuotaEndpoint(method, host, path string) string {
	switch {
	case method == http.MethodGet && host == "api.anthropic.com" && path == "/api/oauth/usage":
		return "claude"
	case method == http.MethodGet && host == "chatgpt.com" && path == "/backend-api/wham/usage":
		return "codex"
	case method == http.MethodPost && host == "cloudcode-pa.googleapis.com" && path == "/v1internal:retrieveUserQuota":
		return "gemini-cli"
	case method == http.MethodPost && isAntigravityQuotaHost(host) && path == "/v1internal:fetchAvailableModels":
		return "antigravity"
	default:
		return ""
	}
}

func isAntigravityQuotaHost(host string) bool {
	switch host {
	case "daily-cloudcode-pa.googleapis.com", "daily-cloudcode-pa.sandbox.googleapis.com", "cloudcode-pa.googleapis.com":
		return true
	default:
		return false
	}
}

func captureClaudeQuotaSnapshots(store *Store, ts int64, provider, authID string, body []byte) int {
	data, ok := parseJSONMap(body)
	if !ok {
		return 0
	}
	count := 0
	for _, key := range []string{"five_hour", "seven_day", "seven_day_oauth_apps", "seven_day_opus", "seven_day_sonnet", "seven_day_cowork", "iguana_necktie"} {
		window, ok := getMap(data, key)
		if !ok {
			continue
		}
		used, ok := getFloatAny(window["utilization"])
		if !ok {
			continue
		}
		store.InsertQuotaSnapshot(ts, provider, authID, key, clampPercent(used), resetAtFromAny(window["resets_at"]))
		count++
	}
	if count > 0 {
		return count
	}
	return captureGenericUsedPercentSnapshots(store, ts, provider, authID, data)
}

func captureCodexQuotaSnapshots(store *Store, ts int64, provider, authID string, body []byte) int {
	data, ok := parseJSONMap(body)
	if !ok {
		return 0
	}
	count := 0
	if windows, ok := data["windows"].([]any); ok {
		for i, item := range windows {
			window, ok := item.(map[string]any)
			if !ok {
				continue
			}
			windowType := firstString(window, "window_type", "windowType")
			if windowType == "" {
				windowType = fmt.Sprintf("window_%d", i+1)
			}
			if insertQuotaWindow(store, ts, provider, authID, windowType, window, false) {
				count++
			}
		}
	}
	if rateLimit, ok := getMap(data, "rate_limit"); ok {
		count += captureCodexRateLimit(store, ts, provider, authID, "code", rateLimit)
	}
	if rateLimit, ok := getMap(data, "code_review_rate_limit"); ok {
		count += captureCodexRateLimit(store, ts, provider, authID, "code_review", rateLimit)
	}
	if limits, ok := data["additional_rate_limits"].([]any); ok {
		for i, item := range limits {
			limitItem, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rateLimit, ok := getMap(limitItem, "rate_limit")
			if !ok {
				continue
			}
			name := firstString(limitItem, "limit_name", "limitName", "metered_feature", "meteredFeature")
			if name == "" {
				name = fmt.Sprintf("additional_%d", i+1)
			}
			count += captureCodexRateLimit(store, ts, provider, authID, "additional_"+sanitizeWindowPart(name), rateLimit)
		}
	}
	return count
}

func captureCodexRateLimit(store *Store, ts int64, provider, authID, prefix string, rateLimit map[string]any) int {
	count := 0
	fallbackUsed := getBoolAny(rateLimit["limit_reached"]) || getBoolAny(rateLimit["limitReached"]) || isFalseBool(rateLimit["allowed"])
	if window, ok := getMap(rateLimit, "primary_window"); ok {
		if insertQuotaWindow(store, ts, provider, authID, codexWindowType(prefix, "primary", window), window, fallbackUsed) {
			count++
		}
	}
	if window, ok := getMap(rateLimit, "secondary_window"); ok {
		if insertQuotaWindow(store, ts, provider, authID, codexWindowType(prefix, "secondary", window), window, fallbackUsed) {
			count++
		}
	}
	return count
}

func codexWindowType(prefix, fallback string, window map[string]any) string {
	seconds, ok := getFloatAny(firstValue(window, "limit_window_seconds", "limitWindowSeconds"))
	if ok {
		switch int64(seconds) {
		case 18000:
			return prefix + "_five_hour"
		case 604800:
			return prefix + "_weekly"
		}
	}
	return prefix + "_" + fallback
}

func insertQuotaWindow(store *Store, ts int64, provider, authID, windowType string, window map[string]any, fallbackUsed bool) bool {
	used, ok := getFloatAny(firstValue(window, "used_percent", "usedPercent"))
	if !ok {
		if !fallbackUsed {
			return false
		}
		used = 100
	}
	store.InsertQuotaSnapshot(ts, provider, authID, windowType, clampPercent(used), resetAtFromAny(firstValue(window, "reset_at", "resetAt", "reset_time", "resetTime")))
	return true
}

func captureGeminiCLIQuotaSnapshots(store *Store, ts int64, provider, authID string, body []byte) int {
	data, ok := parseJSONMap(body)
	if !ok {
		return 0
	}
	buckets, ok := data["buckets"].([]any)
	if !ok {
		return captureGenericUsedPercentSnapshots(store, ts, provider, authID, data)
	}
	count := 0
	for i, item := range buckets {
		bucket, ok := item.(map[string]any)
		if !ok {
			continue
		}
		modelID := firstString(bucket, "modelId", "model_id")
		windowType := modelID
		if tokenType := firstString(bucket, "tokenType", "token_type"); tokenType != "" {
			windowType = strings.Trim(windowType+":"+tokenType, ":")
		}
		if windowType == "" {
			windowType = fmt.Sprintf("bucket_%d", i+1)
		}
		used, ok := usedPercentFromRemainingFraction(bucket)
		resetAt := resetAtFromAny(firstValue(bucket, "resetTime", "reset_time"))
		if !ok {
			if remainingAmount, amountOK := getFloatAny(firstValue(bucket, "remainingAmount", "remaining_amount")); amountOK && remainingAmount <= 0 && resetAt > 0 {
				used = 100
				ok = true
			}
		}
		if !ok {
			continue
		}
		store.InsertQuotaSnapshot(ts, provider, authID, windowType, clampPercent(used), resetAt)
		count++
	}
	return count
}

func captureAntigravityQuotaSnapshots(store *Store, ts int64, provider, authID string, body []byte) int {
	data, ok := parseJSONMap(body)
	if !ok {
		return 0
	}
	models, ok := getMap(data, "models")
	if !ok {
		return 0
	}
	count := 0
	for modelID, item := range models {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		quotaInfo, ok := getMap(model, "quotaInfo")
		if !ok {
			quotaInfo, ok = getMap(model, "quota_info")
		}
		if !ok {
			continue
		}
		used, usedOK := usedPercentFromRemainingFraction(quotaInfo)
		resetAt := resetAtFromAny(firstValue(quotaInfo, "resetTime", "reset_time"))
		if !usedOK && resetAt > 0 {
			used = 100
			usedOK = true
		}
		if !usedOK {
			continue
		}
		store.InsertQuotaSnapshot(ts, provider, authID, modelID, clampPercent(used), resetAt)
		count++
	}
	return count
}

func captureGenericUsedPercentSnapshots(store *Store, ts int64, provider, authID string, data map[string]any) int {
	if v, ok := getFloat(data, "used_percent"); ok {
		store.InsertQuotaSnapshot(ts, provider, authID, "default", clampPercent(v), resetAtFromAny(firstValue(data, "reset_at", "resetAt")))
		return 1
	}
	count := 0
	for k, val := range data {
		if sub, ok := val.(map[string]any); ok {
			if v, ok := getFloat(sub, "used_percent"); ok {
				store.InsertQuotaSnapshot(ts, provider, authID, k, clampPercent(v), resetAtFromAny(firstValue(sub, "reset_at", "resetAt")))
				count++
			}
		}
	}
	return count
}

func usedPercentFromRemainingFraction(m map[string]any) (float64, bool) {
	remaining, ok := getFloatAny(firstValue(m, "remainingFraction", "remaining_fraction", "remaining"))
	if !ok {
		return 0, false
	}
	if remaining > 1 {
		remaining = remaining / 100
	}
	return (1 - remaining) * 100, true
}

func parseJSONMap(body []byte) (map[string]any, bool) {
	var data map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return nil, false
	}
	return data, true
}

func getMap(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	out, ok := v.(map[string]any)
	return out, ok
}

func firstValue(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	return getFloatAny(v)
}

func getFloatAny(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		var num json.Number = json.Number(strings.TrimSpace(n))
		f, err := num.Float64()
		return f, err == nil
	}
	return 0, false
}

func getBoolAny(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "1", "yes", "y", "on":
			return true
		}
	case float64:
		return b != 0
	case json.Number:
		i, err := b.Int64()
		return err == nil && i != 0
	}
	return false
}

func isFalseBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return !b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "false", "0", "no", "n", "off":
			return true
		}
	case float64:
		return b == 0
	case json.Number:
		i, err := b.Int64()
		return err == nil && i == 0
	}
	return false
}

func resetAtFromAny(v any) int64 {
	switch raw := v.(type) {
	case json.Number:
		if i, err := raw.Int64(); err == nil {
			return i
		}
	case float64:
		return int64(raw)
	case int64:
		return raw
	case string:
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return 0
		}
		if n := json.Number(trimmed); n.String() != "" {
			if i, err := n.Int64(); err == nil {
				return i
			}
		}
		if ts, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return ts.Unix()
		}
	}
	return 0
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func sanitizeWindowPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('_')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "_")
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
