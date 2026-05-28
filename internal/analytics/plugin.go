package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

func init() {
	coreusage.RegisterPlugin(&analyticsPlugin{})
}

type analyticsPlugin struct{}

func (p *analyticsPlugin) HandleUsage(ctx context.Context, r coreusage.Record) {
	store := Get()
	if store == nil {
		return
	}

	success := !r.Failed
	ts := r.RequestedAt.Unix()
	clientKeyID := ClientKeyIDFromContext(ctx)
	logClientKeyAttribution(store, ctx, r, ts, clientKeyID)

	cachedTokens := r.Detail.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = r.Detail.CacheReadTokens
	}

	store.InsertQueryLog(ts, clientKeyID, r.Provider, r.AuthID, r.Model,
		r.Detail.InputTokens, r.Detail.OutputTokens, cachedTokens,
		r.Detail.TotalTokens, success,
		r.Detail.ReasoningTokens, r.Detail.CacheReadTokens, r.Detail.CacheCreationTokens)

	hourTS := r.RequestedAt.Truncate(time.Hour).Unix()
	store.UpsertHourlyAggregate(hourTS, clientKeyID, r.Provider, r.AuthID, r.Model,
		r.Detail.InputTokens, r.Detail.OutputTokens, cachedTokens,
		r.Detail.TotalTokens, success,
		r.Detail.ReasoningTokens, r.Detail.CacheReadTokens, r.Detail.CacheCreationTokens)

	if r.Fail.StatusCode == 429 {
		resetAt := r.Fail.ResetAt
		if resetAt <= ts {
			resetAt = store.latestQuotaResetAt(r.Provider, r.AuthID, ts)
		}
		store.InsertQuotaEvent(ts, r.Provider, r.AuthID, r.Model, resetAt)
	}
}

type ginContextReader interface {
	Get(string) (any, bool)
}

func logClientKeyAttribution(store *Store, ctx context.Context, r coreusage.Record, ts int64, clientKeyID int) {
	if store == nil || store.DBPath() == "" {
		return
	}
	entry := map[string]any{
		"ts":                      ts,
		"time":                    time.Unix(ts, 0).Format(time.RFC3339),
		"provider":                r.Provider,
		"model":                   r.Model,
		"auth_id":                 r.AuthID,
		"source_hash":             shortHash(r.Source),
		"record_api_key_hash":     shortHash(r.APIKey),
		"record_has_api_key":      strings.TrimSpace(r.APIKey) != "",
		"ctx_client_key_id":       clientKeyID,
		"ctx_value_type":          "",
		"ctx_value":               "",
		"gin_present":             false,
		"gin_user_api_key_hash":   "",
		"gin_access_provider":     "",
		"gin_metadata_api_key_id": "",
		"failed":                  r.Failed,
		"status_code":             r.Fail.StatusCode,
		"input_tokens":            r.Detail.InputTokens,
		"output_tokens":           r.Detail.OutputTokens,
		"cached_tokens":           r.Detail.CachedTokens,
		"cache_read_tokens":       r.Detail.CacheReadTokens,
		"cache_creation_tokens":   r.Detail.CacheCreationTokens,
		"total_tokens":            r.Detail.TotalTokens,
	}
	if ctx != nil {
		raw := ctx.Value(ClientKeyIDCtxKey)
		if raw != nil {
			entry["ctx_value_type"] = fmt.Sprintf("%T", raw)
			entry["ctx_value"] = fmt.Sprintf("%v", raw)
		}
		if ginCtx, ok := ctx.Value("gin").(ginContextReader); ok && ginCtx != nil {
			entry["gin_present"] = true
			if value, exists := ginCtx.Get("userApiKey"); exists {
				entry["gin_user_api_key_hash"] = shortHash(fmt.Sprintf("%v", value))
			}
			if value, exists := ginCtx.Get("accessProvider"); exists {
				entry["gin_access_provider"] = fmt.Sprintf("%v", value)
			}
			if value, exists := ginCtx.Get("accessMetadata"); exists {
				if metadata, ok := value.(map[string]string); ok {
					entry["gin_metadata_api_key_id"] = metadata["api_key_id"]
				} else {
					entry["gin_metadata_type"] = fmt.Sprintf("%T", value)
				}
			}
		}
	}
	appendJSONLine(filepath.Join(filepath.Dir(store.DBPath()), "client-key-attribution-debug.jsonl"), entry)
}

func appendJSONLine(path string, entry map[string]any) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Debugf("analytics: create debug log dir failed: %v", err)
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		log.Debugf("analytics: marshal debug log failed: %v", err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Debugf("analytics: open debug log failed: %v", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		log.Debugf("analytics: write debug log failed: %v", err)
	}
}

func shortHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
