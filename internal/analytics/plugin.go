package analytics

import (
	"context"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
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
