package analytics

import (
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	tokenMillion                  = 1000000.0
	estimatedQuotaPlateauMaxDelta = 0.5
)

type TokenDimension struct {
	Provider  string
	AuthID    string
	Model     string
	TokenType string
}

type TokenPriceRow struct {
	PriceDate          string   `json:"price_date,omitempty"`
	Provider           string   `json:"provider"`
	AuthID             string   `json:"auth_id"`
	Model              string   `json:"model"`
	TokenType          string   `json:"token_type"`
	PriceUSDPerMillion *float64 `json:"price_usd_per_million"`
	Status             string   `json:"status"`
	Source             string   `json:"source"`
	SourceFromTS       int64    `json:"source_from_ts,omitempty"`
	SourceToTS         int64    `json:"source_to_ts,omitempty"`
}

type ProviderQuotaLinePoint struct {
	HourTS                 int64    `json:"hour_ts"`
	BucketTS               int64    `json:"bucket_ts,omitempty"`
	BucketSeconds          int64    `json:"bucket_seconds,omitempty"`
	QuotaRemainingPercent  float64  `json:"quota_remaining_percent"`
	QuotaUsedPercent       float64  `json:"quota_used_percent"`
	CLIProxyHourUSD        float64  `json:"cliproxy_hour_usd"`
	CLIProxyCumulativeUSD  float64  `json:"cliproxy_cumulative_usd"`
	QuotaEventsCount       int64    `json:"quota_events_count"`
	EstimatedQuotaUSDPoint *float64 `json:"estimated_quota_usd_point,omitempty"`
}

type ProviderQuotaResetMarker struct {
	ResetAt int64   `json:"reset_at"`
	Points  float64 `json:"points"`
}

type ProviderQuotaSeries struct {
	Provider                   string                     `json:"provider"`
	AuthID                     string                     `json:"auth_id"`
	WindowType                 string                     `json:"window_type"`
	MostExpensiveUSDPerMillion float64                    `json:"most_expensive_usd_per_million"`
	InputUSDPerMillion         float64                    `json:"input_usd_per_million"`
	InputPriceModel            string                     `json:"input_price_model"`
	EstimatedQuotaUSD          float64                    `json:"estimated_quota_usd"`
	Points                     []ProviderQuotaLinePoint   `json:"points"`
	ResetMarkers               []ProviderQuotaResetMarker `json:"reset_markers"`
}

type ProviderQuotaLinesResponse struct {
	Series []ProviderQuotaSeries `json:"series"`
}

type quotaSeriesKey struct {
	provider string
	authID   string
}

type inputPriceByAuth struct {
	price float64
	model string
}

type officialPriceRule struct {
	Provider      string
	Patterns      []string
	Input         float64
	Output        float64
	Cached        *float64
	CacheCreation *float64
}

func tokenPrice(v float64) *float64 {
	return &v
}

var officialPriceRules = []officialPriceRule{
	{Provider: "claude", Patterns: []string{"claude-opus-4-8", "opus-4-8"}, Input: 5, Output: 25, Cached: tokenPrice(0.5), CacheCreation: tokenPrice(10)},
	{Provider: "claude", Patterns: []string{"claude-opus-4-7", "opus-4-7"}, Input: 5, Output: 25, Cached: tokenPrice(0.5), CacheCreation: tokenPrice(10)},
	{Provider: "claude", Patterns: []string{"claude-opus-4-6", "opus-4-6"}, Input: 5, Output: 25, Cached: tokenPrice(0.5), CacheCreation: tokenPrice(10)},
	{Provider: "claude", Patterns: []string{"claude-opus-4-5", "opus-4-5"}, Input: 5, Output: 25, Cached: tokenPrice(0.5), CacheCreation: tokenPrice(10)},
	{Provider: "claude", Patterns: []string{"claude-opus-4-1", "opus-4-1"}, Input: 15, Output: 75, Cached: tokenPrice(1.5), CacheCreation: tokenPrice(30)},
	{Provider: "claude", Patterns: []string{"claude-opus-4", "opus-4", "claude-opus", "opus"}, Input: 15, Output: 75, Cached: tokenPrice(1.5), CacheCreation: tokenPrice(30)},
	{Provider: "claude", Patterns: []string{"claude-sonnet-4-6", "sonnet-4-6"}, Input: 3, Output: 15, Cached: tokenPrice(0.3), CacheCreation: tokenPrice(6)},
	{Provider: "claude", Patterns: []string{"claude-sonnet-4-5", "sonnet-4-5"}, Input: 3, Output: 15, Cached: tokenPrice(0.3), CacheCreation: tokenPrice(6)},
	{Provider: "claude", Patterns: []string{"claude-sonnet-4", "sonnet-4", "claude-sonnet", "sonnet"}, Input: 3, Output: 15, Cached: tokenPrice(0.3), CacheCreation: tokenPrice(6)},
	{Provider: "claude", Patterns: []string{"claude-haiku-4-5", "haiku-4-5"}, Input: 1, Output: 5, Cached: tokenPrice(0.1), CacheCreation: tokenPrice(2)},
	{Provider: "claude", Patterns: []string{"claude-haiku-3-5", "haiku-3-5"}, Input: 0.8, Output: 4, Cached: tokenPrice(0.08), CacheCreation: tokenPrice(1.6)},
	{Provider: "claude", Patterns: []string{"claude-haiku", "haiku"}, Input: 1, Output: 5, Cached: tokenPrice(0.1), CacheCreation: tokenPrice(2)},
	{Provider: "codex", Patterns: []string{"gpt-5.5-pro"}, Input: 30, Output: 180},
	{Provider: "openai", Patterns: []string{"gpt-5.5-pro"}, Input: 30, Output: 180},
	{Provider: "codex", Patterns: []string{"gpt-5.5"}, Input: 5, Output: 30, Cached: tokenPrice(0.5)},
	{Provider: "openai", Patterns: []string{"gpt-5.5"}, Input: 5, Output: 30, Cached: tokenPrice(0.5)},
	{Provider: "codex", Patterns: []string{"codex-auto-review", "gpt-5.3-codex"}, Input: 1.75, Output: 14, Cached: tokenPrice(0.175)},
	{Provider: "openai", Patterns: []string{"gpt-5.3-codex"}, Input: 1.75, Output: 14, Cached: tokenPrice(0.175)},
	{Provider: "codex", Patterns: []string{"gpt-image-2"}, Input: 5, Output: 30, Cached: tokenPrice(1.25)},
	{Provider: "openai", Patterns: []string{"gpt-image-2"}, Input: 5, Output: 30, Cached: tokenPrice(1.25)},
	{Provider: "codex", Patterns: []string{"gpt-5.4-pro"}, Input: 30, Output: 180},
	{Provider: "openai", Patterns: []string{"gpt-5.4-pro"}, Input: 30, Output: 180},
	{Provider: "codex", Patterns: []string{"gpt-5.4-mini"}, Input: 0.75, Output: 4.5, Cached: tokenPrice(0.075)},
	{Provider: "openai", Patterns: []string{"gpt-5.4-mini"}, Input: 0.75, Output: 4.5, Cached: tokenPrice(0.075)},
	{Provider: "codex", Patterns: []string{"gpt-5.4-nano"}, Input: 0.2, Output: 1.25, Cached: tokenPrice(0.02)},
	{Provider: "openai", Patterns: []string{"gpt-5.4-nano"}, Input: 0.2, Output: 1.25, Cached: tokenPrice(0.02)},
	{Provider: "codex", Patterns: []string{"gpt-5.4"}, Input: 2.5, Output: 15, Cached: tokenPrice(0.25)},
	{Provider: "openai", Patterns: []string{"gpt-5.4"}, Input: 2.5, Output: 15, Cached: tokenPrice(0.25)},
	{Provider: "codex", Patterns: []string{"gpt-5-codex"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "openai", Patterns: []string{"gpt-5-codex"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "codex", Patterns: []string{"gpt-5-mini"}, Input: 0.25, Output: 2, Cached: tokenPrice(0.025)},
	{Provider: "openai", Patterns: []string{"gpt-5-mini"}, Input: 0.25, Output: 2, Cached: tokenPrice(0.025)},
	{Provider: "codex", Patterns: []string{"gpt-5-nano"}, Input: 0.05, Output: 0.4, Cached: tokenPrice(0.005)},
	{Provider: "openai", Patterns: []string{"gpt-5-nano"}, Input: 0.05, Output: 0.4, Cached: tokenPrice(0.005)},
	{Provider: "codex", Patterns: []string{"gpt-5"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "openai", Patterns: []string{"gpt-5"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "codex", Patterns: []string{"gpt-4.1-mini"}, Input: 0.4, Output: 1.6, Cached: tokenPrice(0.1)},
	{Provider: "openai", Patterns: []string{"gpt-4.1-mini"}, Input: 0.4, Output: 1.6, Cached: tokenPrice(0.1)},
	{Provider: "codex", Patterns: []string{"gpt-4.1-nano"}, Input: 0.1, Output: 0.4, Cached: tokenPrice(0.025)},
	{Provider: "openai", Patterns: []string{"gpt-4.1-nano"}, Input: 0.1, Output: 0.4, Cached: tokenPrice(0.025)},
	{Provider: "codex", Patterns: []string{"gpt-4.1"}, Input: 2, Output: 8, Cached: tokenPrice(0.5)},
	{Provider: "openai", Patterns: []string{"gpt-4.1"}, Input: 2, Output: 8, Cached: tokenPrice(0.5)},
	{Provider: "gemini-cli", Patterns: []string{"gemini-3.1-pro-preview", "gemini-3.1-pro"}, Input: 2, Output: 12},
	{Provider: "gemini", Patterns: []string{"gemini-3.1-pro-preview", "gemini-3.1-pro"}, Input: 2, Output: 12},
	{Provider: "gemini-cli", Patterns: []string{"gemini-3-flash-preview", "gemini-3-flash"}, Input: 0.5, Output: 3},
	{Provider: "gemini", Patterns: []string{"gemini-3-flash-preview", "gemini-3-flash"}, Input: 0.5, Output: 3},
	{Provider: "gemini-cli", Patterns: []string{"gemini-2.5-pro"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "gemini", Patterns: []string{"gemini-2.5-pro"}, Input: 1.25, Output: 10, Cached: tokenPrice(0.125)},
	{Provider: "gemini-cli", Patterns: []string{"gemini-2.5-flash-lite"}, Input: 0.1, Output: 0.4, Cached: tokenPrice(0.01)},
	{Provider: "gemini", Patterns: []string{"gemini-2.5-flash-lite"}, Input: 0.1, Output: 0.4, Cached: tokenPrice(0.01)},
	{Provider: "gemini-cli", Patterns: []string{"gemini-2.5-flash"}, Input: 0.3, Output: 2.5, Cached: tokenPrice(0.03)},
	{Provider: "gemini", Patterns: []string{"gemini-2.5-flash"}, Input: 0.3, Output: 2.5, Cached: tokenPrice(0.03)},
}

func officialTokenPriceUSD(provider, model, tokenType string) (float64, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	tokenType = strings.ToLower(strings.TrimSpace(tokenType))
	for _, rule := range officialPriceRules {
		if provider != rule.Provider {
			continue
		}
		for _, pattern := range rule.Patterns {
			if model == pattern || strings.Contains(model, pattern) {
				switch tokenType {
				case "input":
					return rule.Input, true
				case "output":
					return rule.Output, true
				case "cached_input":
					if rule.Cached == nil {
						return 0, false
					}
					return *rule.Cached, true
				case "cache_creation_input":
					if rule.CacheCreation == nil {
						return 0, false
					}
					return *rule.CacheCreation, true
				}
			}
		}
	}
	return 0, false
}

func (s *Store) OfficialTokenPricesForUsage(fromTS, toTS int64) ([]TokenPriceRow, error) {
	if s == nil {
		return nil, nil
	}
	if fromTS <= 0 || toTS <= fromTS {
		toTS = time.Now().Unix()
		fromTS = toTS - 24*60*60
	}
	dims, err := s.usedTokenDimensions(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	rows := make([]TokenPriceRow, 0, len(dims))
	for _, dim := range dims {
		row := TokenPriceRow{
			Provider:     dim.Provider,
			AuthID:       dim.AuthID,
			Model:        dim.Model,
			TokenType:    dim.TokenType,
			Status:       "unknown",
			Source:       "official",
			SourceFromTS: fromTS,
			SourceToTS:   toTS,
		}
		if price, ok := officialTokenPriceUSD(dim.Provider, dim.Model, dim.TokenType); ok {
			row.PriceUSDPerMillion = &price
			row.Status = "official"
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Store) usedTokenDimensions(fromTS, toTS int64) ([]TokenDimension, error) {
	dims := map[TokenDimension]struct{}{}
	queries := []string{
		`SELECT provider, auth_id, model,
		        COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		        COALESCE(SUM(cached_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0)
		 FROM query_logs
		 WHERE ts >= ? AND ts < ? AND provider <> '' AND model <> ''
		 GROUP BY provider, auth_id, model`,
		`SELECT provider, auth_id, model,
		        COALESCE(SUM(input_tokens_sum), 0), COALESCE(SUM(output_tokens_sum), 0),
		        COALESCE(SUM(cached_tokens_sum), 0), COALESCE(SUM(cache_creation_tokens_sum), 0)
		 FROM hourly_aggregates
		 WHERE hour_ts >= ? AND hour_ts < ? AND provider <> '' AND model <> ''
		 GROUP BY provider, auth_id, model`,
	}
	for _, query := range queries {
		rows, err := s.db.Query(query, fromTS, toTS)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var provider, authID, model string
			var input, output, cached, cacheCreation int64
			if err := rows.Scan(&provider, &authID, &model, &input, &output, &cached, &cacheCreation); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if input > 0 {
				dims[TokenDimension{Provider: provider, AuthID: authID, Model: model, TokenType: "input"}] = struct{}{}
			}
			if output > 0 {
				dims[TokenDimension{Provider: provider, AuthID: authID, Model: model, TokenType: "output"}] = struct{}{}
			}
			if cached > 0 {
				dims[TokenDimension{Provider: provider, AuthID: authID, Model: model, TokenType: "cached_input"}] = struct{}{}
			}
			if cacheCreation > 0 {
				dims[TokenDimension{Provider: provider, AuthID: authID, Model: model, TokenType: "cache_creation_input"}] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]TokenDimension, 0, len(dims))
	for dim := range dims {
		out = append(out, dim)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider == out[j].Provider {
			if out[i].AuthID == out[j].AuthID {
				if out[i].Model == out[j].Model {
					return out[i].TokenType < out[j].TokenType
				}
				return out[i].Model < out[j].Model
			}
			return out[i].AuthID < out[j].AuthID
		}
		return out[i].Provider < out[j].Provider
	})
	return out, nil
}

func (s *Store) chooseQuotaWindowForAuthClass(provider, authID string, fromTS, toTS int64, windowClass string) (string, error) {
	rows, err := s.db.Query(`
		SELECT window_type, MAX(ts) FROM quota_snapshots
		WHERE provider = ? AND auth_id = ? AND ts >= ? AND ts < ?
		GROUP BY window_type`, provider, authID, fromTS, toTS)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	return scanPreferredQuotaWindow(rows, windowClass)
}

func scanPreferredQuotaWindow(rows *sql.Rows, windowClass string) (string, error) {
	type entry struct {
		windowType string
		ts         int64
	}
	var all []entry
	for rows.Next() {
		var wt string
		var ts int64
		if err := rows.Scan(&wt, &ts); err != nil {
			return "", err
		}
		all = append(all, entry{windowType: wt, ts: ts})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	pick := func(candidates []entry) string {
		latest := ""
		var latestTS int64
		for _, e := range candidates {
			w := strings.TrimSpace(e.windowType)
			if strings.EqualFold(w, "weekly") {
				return e.windowType
			}
			if strings.EqualFold(w, "default") {
				latest = e.windowType
				latestTS = e.ts
				continue
			}
			if latest == "" || e.ts > latestTS {
				latest = e.windowType
				latestTS = e.ts
			}
		}
		return latest
	}

	if windowClass != "" {
		var matched []entry
		hasClassifiedWindow := false
		for _, e := range all {
			class := classifyQuotaWindow(e.windowType)
			if class != "" {
				hasClassifiedWindow = true
			}
			if class == windowClass {
				matched = append(matched, e)
			}
		}
		if len(matched) > 0 {
			return pick(matched), nil
		}
		if hasClassifiedWindow {
			return "", nil
		}
	}
	return pick(all), nil
}

func classifyQuotaWindow(windowType string) string {
	wt := strings.ToLower(strings.TrimSpace(windowType))
	if wt == "" {
		return ""
	}
	if strings.Contains(wt, "five_hour") || strings.Contains(wt, "fivehour") || strings.Contains(wt, "five-hour") {
		return "5h"
	}
	if strings.Contains(wt, "seven_day") || strings.Contains(wt, "sevenday") || strings.Contains(wt, "seven-day") || strings.Contains(wt, "weekly") {
		return "7d"
	}
	return ""
}

func (s *Store) ProviderQuotaLines(fromTS, toTS int64, resetOn429, resetOnRefresh bool, windowClass string) (*ProviderQuotaLinesResponse, error) {
	if s == nil {
		return &ProviderQuotaLinesResponse{}, nil
	}
	keys, err := s.analyticsAuthRefs(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	bucketSeconds, err := s.quotaLinesBucketSeconds(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	displayStart := floorBucket(fromTS, bucketSeconds)
	priceFromTS := fromTS - 60*86400
	maxPrices, err := s.maxOfficialPriceByAuth(priceFromTS, toTS)
	if err != nil {
		return nil, err
	}
	inputPrices, err := s.maxInputPriceByAuth(priceFromTS, toTS)
	if err != nil {
		return nil, err
	}
	resp := &ProviderQuotaLinesResponse{}
	for _, key := range keys {
		windowType, err := s.chooseQuotaWindowForAuthClass(key.provider, key.authID, fromTS-60*86400, toTS, windowClass)
		if err != nil {
			return nil, err
		}
		if windowType == "" {
			continue
		}
		lookbackFromTS := fromTS - quotaAccumulationLookbackSeconds(windowClass, windowType)
		if lookbackFromTS < 0 {
			lookbackFromTS = 0
		}
		windowDuration := quotaWindowDurationSeconds(windowClass, windowType)
		resetLookupToTS := toTS
		if windowDuration > 0 {
			resetLookupToTS += windowDuration
		}
		bucketUSD, err := s.usdByAuthBucket(lookbackFromTS, toTS, bucketSeconds)
		if err != nil {
			return nil, err
		}
		events, err := s.eventCountsByAuthBucket(lookbackFromTS, toTS, bucketSeconds)
		if err != nil {
			return nil, err
		}
		eventResetMarkers, err := s.eventResetMarkersByAuth(lookbackFromTS, resetLookupToTS)
		if err != nil {
			return nil, err
		}
		usedByBucket, rawUsedByBucket, err := s.quotaUsedByBucket(key.provider, key.authID, windowType, lookbackFromTS, toTS, bucketSeconds)
		if err != nil {
			return nil, err
		}
		_, hourlyRawUsedByBucket, err := s.quotaUsedByBucket(key.provider, key.authID, windowType, lookbackFromTS, toTS, analyticsHourBucketSeconds)
		if err != nil {
			return nil, err
		}
		estimateFromTS, estimateBucketSeconds, estimateMinDelta := quotaEstimateWindow(windowClass, windowType, fromTS, toTS)
		estimateBucketUSD, err := s.usdByAuthBucket(estimateFromTS, toTS, estimateBucketSeconds)
		if err != nil {
			return nil, err
		}
		estimateUsedByBucket, _, err := s.quotaUsedByBucket(key.provider, key.authID, windowType, estimateFromTS, toTS+estimateBucketSeconds, estimateBucketSeconds)
		if err != nil {
			return nil, err
		}
		inputPrice := inputPrices[key]
		series := ProviderQuotaSeries{
			Provider:                   key.provider,
			AuthID:                     key.authID,
			WindowType:                 windowType,
			MostExpensiveUSDPerMillion: maxPrices[key],
			InputUSDPerMillion:         inputPrice.price,
			InputPriceModel:            inputPrice.model,
		}
		snapshotResetMarkers, err := s.snapshotResetMarkersForWindow(key.provider, key.authID, windowType, lookbackFromTS, resetLookupToTS)
		if err != nil {
			return nil, err
		}
		resetTimes := dedupeResetTimesByBucket(mergeResetTimes(eventResetMarkers[key], snapshotResetMarkers), 60)
		accumulationStart := quotaCycleAccumulationStart(resetTimes, displayStart, bucketSeconds, windowDuration)
		if resetOn429 {
			if eventBucket := latestEventBucketBeforeOrAt(events[key], displayStart); eventBucket > accumulationStart {
				accumulationStart = eventBucket
			}
		}
		hourlyPlateauBuckets := quotaUsedPlateauBuckets(hourlyRawUsedByBucket)
		var cumulative, lastUsed float64
		var lastUsedBucket int64
		var haveLast bool
		for bucket := accumulationStart; bucket < toTS; bucket += bucketSeconds {
			used, ok := usedByBucket[bucket]
			freshUsed, freshOK := rawUsedByBucket[bucket]
			if freshOK && haveLast && quotaUsageDropLooksLikeReset(lastUsed, freshUsed, bucket-lastUsedBucket) {
				cumulative = 0
			}
			if freshOK {
				lastUsed = freshUsed
				lastUsedBucket = bucket
				haveLast = true
			} else if !ok && haveLast {
				used = lastUsed
			}
			eventCount := events[key][bucket]
			if resetOn429 && eventCount > 0 {
				cumulative = 0
			}
			if resetOnRefresh && hasResetInBucket(resetTimes, bucket, bucketSeconds) {
				cumulative = 0
			}
			bucketValue := bucketUSD[key][bucket]
			cumulative += bucketValue
			if bucket < displayStart {
				continue
			}
			point := ProviderQuotaLinePoint{
				HourTS:                bucket,
				BucketTS:              bucket,
				BucketSeconds:         bucketSeconds,
				QuotaRemainingPercent: math.Max(0, 100-used),
				QuotaUsedPercent:      math.Max(0, used),
				CLIProxyHourUSD:       bucketValue,
				CLIProxyCumulativeUSD: cumulative,
				QuotaEventsCount:      eventCount,
			}
			hourBucket := floorBucket(bucket, analyticsHourBucketSeconds)
			hourUsed, hourUsedOK := hourlyRawUsedByBucket[hourBucket]
			if hourUsedOK && hourlyPlateauBuckets[hourBucket] && bucket+bucketSeconds >= hourBucket+analyticsHourBucketSeconds && hourUsed >= 10 && hourUsed <= 100 && cumulative > 0 {
				est := cumulative / hourUsed * 100
				point.EstimatedQuotaUSDPoint = &est
			}
			series.Points = append(series.Points, point)
		}
		series.ResetMarkers = buildResetMarkers(filterResetTimes(resetTimes, fromTS, toTS), series.Points)
		series.EstimatedQuotaUSD = estimateQuotaUSDFromBuckets(estimateFromTS, toTS, estimateBucketSeconds, estimateMinDelta, estimateUsedByBucket, estimateBucketUSD[key])
		resp.Series = append(resp.Series, series)
	}
	return resp, nil
}

func (s *Store) quotaLinesBucketSeconds(fromTS, toTS int64) (int64, error) {
	var hasRaw int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM query_logs WHERE ts >= ? AND ts < ? LIMIT 1)`, fromTS, toTS).Scan(&hasRaw); err != nil {
		return 0, err
	}
	if hasRaw != 0 {
		return analyticsFiveMinuteBucketSeconds, nil
	}
	return analyticsBucketSeconds(fromTS, toTS), nil
}

func quotaEstimateWindow(windowClass, windowType string, fromTS, toTS int64) (int64, int64, float64) {
	class := classifyQuotaWindow(windowType)
	if class == "" {
		class = windowClass
	}
	if class == "7d" {
		return toTS - 8*24*60*60, analyticsFiveMinuteBucketSeconds, 3
	}
	return toTS - analyticsFineBucketMaxRange, analyticsFiveMinuteBucketSeconds, 1
}

func quotaAccumulationLookbackSeconds(windowClass, windowType string) int64 {
	class := classifyQuotaWindow(windowType)
	if class == "" {
		class = windowClass
	}
	switch class {
	case "5h":
		return int64((6 * time.Hour) / time.Second)
	case "7d":
		return int64((8 * 24 * time.Hour) / time.Second)
	default:
		return 60 * 24 * 60 * 60
	}
}

func quotaWindowDurationSeconds(windowClass, windowType string) int64 {
	class := classifyQuotaWindow(windowType)
	if class == "" {
		class = windowClass
	}
	switch class {
	case "5h":
		return int64((5 * time.Hour) / time.Second)
	case "7d":
		return int64((7 * 24 * time.Hour) / time.Second)
	default:
		return 0
	}
}

func quotaCycleAccumulationStart(resetTimes []int64, displayStart, bucketSeconds, windowDuration int64) int64 {
	if resetAt := latestResetBeforeOrAt(resetTimes, displayStart); resetAt > 0 {
		return floorBucket(resetAt, bucketSeconds)
	}
	if windowDuration <= 0 {
		return displayStart
	}
	if resetAt := earliestResetAfter(resetTimes, displayStart); resetAt > 0 {
		cycleStart := resetAt - windowDuration
		if cycleStart <= displayStart {
			return floorBucket(cycleStart, bucketSeconds)
		}
	}
	return displayStart
}

func latestResetBeforeOrAt(resetTimes []int64, ts int64) int64 {
	var latest int64
	for _, resetAt := range resetTimes {
		if resetAt <= ts && resetAt > latest {
			latest = resetAt
		}
	}
	return latest
}

func earliestResetAfter(resetTimes []int64, ts int64) int64 {
	var earliest int64
	for _, resetAt := range resetTimes {
		if resetAt <= ts {
			continue
		}
		if earliest == 0 || resetAt < earliest {
			earliest = resetAt
		}
	}
	return earliest
}

func latestEventBucketBeforeOrAt(events map[int64]int64, ts int64) int64 {
	var latest int64
	for bucket, count := range events {
		if count > 0 && bucket <= ts && bucket > latest {
			latest = bucket
		}
	}
	return latest
}

func filterResetTimes(resetTimes []int64, fromTS, toTS int64) []int64 {
	out := make([]int64, 0, len(resetTimes))
	for _, resetAt := range resetTimes {
		if resetAt >= fromTS && resetAt < toTS {
			out = append(out, resetAt)
		}
	}
	return out
}

func quotaUsageDropLooksLikeReset(previous, current float64, deltaSeconds int64) bool {
	if current >= previous {
		return false
	}
	if current < 3 {
		return true
	}
	return deltaSeconds <= analyticsFiveMinuteBucketSeconds && previous-current > 5
}

func estimateQuotaUSDFromBuckets(fromTS, toTS, bucketSeconds int64, minPercentDelta float64, usedByBucket map[int64]float64, usdByBucket map[int64]float64) float64 {
	var totalUSD, totalDelta float64
	var segmentUSD, segmentDelta float64
	for bucket := floorBucket(fromTS, bucketSeconds); bucket+bucketSeconds <= toTS; bucket += bucketSeconds {
		startUsed, startOK := usedByBucket[bucket]
		endUsed, endOK := usedByBucket[bucket+bucketSeconds]
		if !startOK || !endOK {
			continue
		}
		percentDelta := endUsed - startUsed
		if percentDelta < 0 {
			if segmentDelta >= minPercentDelta && segmentUSD > 0 {
				totalUSD += segmentUSD
				totalDelta += segmentDelta
			}
			segmentUSD = 0
			segmentDelta = 0
			continue
		}
		if usd := usdByBucket[bucket]; usd > 0 {
			segmentUSD += usd
		}
		segmentDelta += percentDelta
	}
	if segmentDelta >= minPercentDelta && segmentUSD > 0 {
		totalUSD += segmentUSD
		totalDelta += segmentDelta
	}
	if totalDelta < minPercentDelta || totalUSD <= 0 {
		return 0
	}
	estimate := totalUSD / totalDelta * 100
	if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		return 0
	}
	return estimate
}

func (s *Store) analyticsAuthRefs(fromTS, toTS int64) ([]quotaSeriesKey, error) {
	rows, err := s.db.Query(`
		SELECT provider, auth_id FROM hourly_aggregates WHERE hour_ts >= ? AND hour_ts < ? AND provider <> ''
		UNION
		SELECT provider, auth_id FROM query_logs WHERE ts >= ? AND ts < ? AND provider <> ''
		UNION
		SELECT provider, auth_id FROM quota_snapshots WHERE ts >= ? AND ts < ? AND provider <> ''
		UNION
		SELECT provider, auth_id FROM quota_exhaustion_events WHERE ((ts >= ? AND ts < ?) OR (reset_at >= ? AND reset_at < ?)) AND provider <> ''
		ORDER BY provider, auth_id`, fromTS, toTS, fromTS, toTS, fromTS, toTS, fromTS, toTS, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []quotaSeriesKey
	for rows.Next() {
		var key quotaSeriesKey
		if err := rows.Scan(&key.provider, &key.authID); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) usdByAuthBucket(fromTS, toTS, bucketSeconds int64) (map[quotaSeriesKey]map[int64]float64, error) {
	if bucketSeconds == analyticsFiveMinuteBucketSeconds {
		return s.queryLogBucketUSD(fromTS, toTS, bucketSeconds)
	}
	return s.hourlyBucketUSD(fromTS, toTS, bucketSeconds)
}

func (s *Store) hourlyEstimateUSDByAuthBucket(fromTS, toTS, displayBucketSeconds int64) (map[quotaSeriesKey]map[int64]float64, error) {
	if displayBucketSeconds == analyticsFiveMinuteBucketSeconds {
		return s.queryLogBucketUSD(fromTS, toTS, analyticsHourBucketSeconds)
	}
	return s.hourlyBucketUSD(fromTS, toTS, analyticsHourBucketSeconds)
}

func (s *Store) queryLogBucketUSD(fromTS, toTS, bucketSeconds int64) (map[quotaSeriesKey]map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket_ts,provider,auth_id,model,
		       SUM(input_tokens),SUM(output_tokens),SUM(cached_tokens),
		       SUM(reasoning_tokens),SUM(cache_read_tokens),SUM(cache_creation_tokens)
		FROM query_logs
		WHERE ts >= ? AND ts < ?
		GROUP BY bucket_ts,provider,auth_id,model`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUSDRows(rows)
}

func (s *Store) hourlyBucketUSD(fromTS, toTS, bucketSeconds int64) (map[quotaSeriesKey]map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (hour_ts / ?) * ? AS bucket_ts,provider,auth_id,model,
		       SUM(input_tokens_sum),SUM(output_tokens_sum),SUM(cached_tokens_sum),
		       SUM(reasoning_tokens_sum),SUM(cache_read_tokens_sum),SUM(cache_creation_tokens_sum)
		FROM hourly_aggregates
		WHERE hour_ts >= ? AND hour_ts < ?
		GROUP BY bucket_ts,provider,auth_id,model`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUSDRows(rows)
}

func scanUSDRows(rows *sql.Rows) (map[quotaSeriesKey]map[int64]float64, error) {
	out := map[quotaSeriesKey]map[int64]float64{}
	for rows.Next() {
		var bucket int64
		var key quotaSeriesKey
		var model string
		var input, output, cached, reasoning, cacheRead, cacheCreation int64
		if err := rows.Scan(&bucket, &key.provider, &key.authID, &model, &input, &output, &cached, &reasoning, &cacheRead, &cacheCreation); err != nil {
			return nil, err
		}
		usd := tokenCostUSD(key.provider, model, input, output, cached, reasoning, cacheRead, cacheCreation)
		if out[key] == nil {
			out[key] = map[int64]float64{}
		}
		out[key][bucket] += usd
	}
	return out, rows.Err()
}

// tokenCostUSD computes the official-price USD cost for one bucket of token usage.
//
// Each provider's upstream usage schema decomposes tokens differently, so we
// dispatch by provider style:
//
//   - Anthropic (Claude): input_tokens / cache_read / cache_creation are
//     mutually exclusive categories. Output already excludes any reasoning
//     surfaced through this API, so we just bill the four buckets directly
//     with their own per-million prices.
//   - OpenAI (Codex, OpenAI): prompt_tokens INCLUDES cached_tokens and
//     completion_tokens INCLUDES reasoning_tokens. To avoid double-counting
//     cached tokens we subtract them from input before applying the input
//     price; reasoning is already billed at the output rate via output_tokens.
//   - Gemini (gemini, gemini-cli, vertex, antigravity, aistudio):
//     promptTokenCount INCLUDES cachedContentTokenCount but candidatesTokenCount
//     EXCLUDES thoughtsTokenCount. Subtract cached from input, and bill
//     reasoning tokens separately at the output rate, matching Google's billing.
//
// Negative differences (e.g. cached > input from a malformed upstream record)
// are clamped to zero rather than producing negative USD.
func tokenCostUSD(provider, model string, input, output, cached, reasoning, cacheRead, cacheCreation int64) float64 {
	switch costStyleForProvider(provider) {
	case costStyleAnthropic:
		return anthropicTokenCostUSD(provider, model, input, output, cacheRead, cacheCreation)
	case costStyleGemini:
		return geminiTokenCostUSD(provider, model, input, output, cached, reasoning)
	default:
		return openAITokenCostUSD(provider, model, input, output, cached)
	}
}

type providerCostStyle int

const (
	costStyleOpenAI providerCostStyle = iota
	costStyleAnthropic
	costStyleGemini
)

func costStyleForProvider(provider string) providerCostStyle {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return costStyleAnthropic
	case "gemini", "gemini-cli", "vertex", "antigravity", "aistudio":
		return costStyleGemini
	default:
		return costStyleOpenAI
	}
}

func anthropicTokenCostUSD(provider, model string, input, output, cacheRead, cacheCreation int64) float64 {
	var total float64
	if price, ok := officialTokenPriceUSD(provider, model, "input"); ok {
		total += float64(clampNonNegative(input)) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "output"); ok {
		total += float64(clampNonNegative(output)) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "cached_input"); ok {
		total += float64(clampNonNegative(cacheRead)) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "cache_creation_input"); ok {
		total += float64(clampNonNegative(cacheCreation)) / tokenMillion * price
	}
	return total
}

func openAITokenCostUSD(provider, model string, input, output, cached int64) float64 {
	uncached := clampNonNegative(input - cached)
	cached = clampNonNegative(cached)
	output = clampNonNegative(output)
	var total float64
	if price, ok := officialTokenPriceUSD(provider, model, "input"); ok {
		total += float64(uncached) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "output"); ok {
		total += float64(output) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "cached_input"); ok {
		total += float64(cached) / tokenMillion * price
	}
	return total
}

func geminiTokenCostUSD(provider, model string, input, output, cached, reasoning int64) float64 {
	uncached := clampNonNegative(input - cached)
	cached = clampNonNegative(cached)
	output = clampNonNegative(output)
	reasoning = clampNonNegative(reasoning)
	var total float64
	if price, ok := officialTokenPriceUSD(provider, model, "input"); ok {
		total += float64(uncached) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "output"); ok {
		total += float64(output+reasoning) / tokenMillion * price
	}
	if price, ok := officialTokenPriceUSD(provider, model, "cached_input"); ok {
		total += float64(cached) / tokenMillion * price
	}
	return total
}

func clampNonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func (s *Store) maxOfficialPriceByAuth(fromTS, toTS int64) (map[quotaSeriesKey]float64, error) {
	dims, err := s.usedTokenDimensions(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	out := map[quotaSeriesKey]float64{}
	for _, dim := range dims {
		price, ok := officialTokenPriceUSD(dim.Provider, dim.Model, dim.TokenType)
		if !ok {
			continue
		}
		key := quotaSeriesKey{provider: dim.Provider, authID: dim.AuthID}
		if price > out[key] {
			out[key] = price
		}
	}
	return out, nil
}

func (s *Store) maxInputPriceByAuth(fromTS, toTS int64) (map[quotaSeriesKey]inputPriceByAuth, error) {
	dims, err := s.usedTokenDimensions(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	out := map[quotaSeriesKey]inputPriceByAuth{}
	for _, dim := range dims {
		key := quotaSeriesKey{provider: dim.Provider, authID: dim.AuthID}
		current := out[key]
		price, ok := officialTokenPriceUSD(dim.Provider, dim.Model, "input")
		if !ok {
			if current.price == 0 && (current.model == "" || dim.Model < current.model) {
				out[key] = inputPriceByAuth{price: current.price, model: dim.Model}
			}
			continue
		}
		if price > current.price || (price == current.price && (current.model == "" || dim.Model < current.model)) {
			out[key] = inputPriceByAuth{price: price, model: dim.Model}
		}
	}
	return out, nil
}

func (s *Store) eventCountsByAuthBucket(fromTS, toTS, bucketSeconds int64) (map[quotaSeriesKey]map[int64]int64, error) {
	rows, err := s.db.Query(`
		SELECT provider, auth_id, (ts / ?) * ? AS bucket_ts, COUNT(*)
		FROM quota_exhaustion_events
		WHERE ts >= ? AND ts < ?
		GROUP BY provider, auth_id, bucket_ts`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[quotaSeriesKey]map[int64]int64{}
	for rows.Next() {
		var key quotaSeriesKey
		var bucket, count int64
		if err := rows.Scan(&key.provider, &key.authID, &bucket, &count); err != nil {
			return nil, err
		}
		if out[key] == nil {
			out[key] = map[int64]int64{}
		}
		out[key][bucket] = count
	}
	return out, rows.Err()
}

func (s *Store) eventResetMarkersByAuth(fromTS, toTS int64) (map[quotaSeriesKey][]int64, error) {
	rows, err := s.db.Query(`
		SELECT provider, auth_id, reset_at
		FROM quota_exhaustion_events
		WHERE reset_at >= ? AND reset_at < ? AND reset_at > 0 AND provider <> ''
		GROUP BY provider, auth_id, reset_at
		ORDER BY provider, auth_id, reset_at`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[quotaSeriesKey][]int64{}
	for rows.Next() {
		var key quotaSeriesKey
		var resetAt int64
		if err := rows.Scan(&key.provider, &key.authID, &resetAt); err != nil {
			return nil, err
		}
		out[key] = append(out[key], resetAt)
	}
	return out, rows.Err()
}

func (s *Store) snapshotResetMarkersForWindow(provider, authID, windowType string, fromTS, toTS int64) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT MIN(reset_at)
		FROM quota_snapshots
		WHERE provider = ? AND auth_id = ? AND window_type = ? AND reset_at >= ? AND reset_at < ? AND reset_at > 0 AND used_percent >= 2
		GROUP BY reset_at / 60
		ORDER BY MIN(reset_at)`, provider, authID, windowType, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var resetAt int64
		if err := rows.Scan(&resetAt); err != nil {
			return nil, err
		}
		out = append(out, resetAt)
	}
	return out, rows.Err()
}

func mergeResetTimes(groups ...[]int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, group := range groups {
		for _, resetAt := range group {
			if resetAt <= 0 || seen[resetAt] {
				continue
			}
			seen[resetAt] = true
			out = append(out, resetAt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func dedupeResetTimesByBucket(resetTimes []int64, bucketSeconds int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, resetAt := range resetTimes {
		bucket := floorBucket(resetAt, bucketSeconds)
		if seen[bucket] {
			continue
		}
		seen[bucket] = true
		out = append(out, resetAt)
	}
	return out
}

func buildResetMarkers(resetTimes []int64, points []ProviderQuotaLinePoint) []ProviderQuotaResetMarker {
	markers := make([]ProviderQuotaResetMarker, 0, len(resetTimes))
	for _, resetAt := range resetTimes {
		markers = append(markers, ProviderQuotaResetMarker{ResetAt: resetAt, Points: nearestQuotaUsedPercent(resetAt, points)})
	}
	return markers
}

func nearestQuotaUsedPercent(ts int64, points []ProviderQuotaLinePoint) float64 {
	if len(points) == 0 {
		return 0
	}
	best := points[0]
	bestDistance := absInt64(best.BucketTS - ts)
	for _, point := range points[1:] {
		distance := absInt64(point.BucketTS - ts)
		if distance < bestDistance {
			best = point
			bestDistance = distance
		}
	}
	return best.QuotaUsedPercent
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func hasResetInBucket(resetTimes []int64, bucketTS, bucketSeconds int64) bool {
	for _, resetAt := range resetTimes {
		if floorBucket(resetAt, bucketSeconds) == bucketTS {
			return true
		}
	}
	return false
}

func (s *Store) quotaUsedByBucket(provider, authID, windowType string, fromTS, toTS, bucketSeconds int64) (map[int64]float64, map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket_ts, used_percent
		FROM quota_snapshots
		WHERE provider = ? AND auth_id = ? AND window_type = ? AND ts >= ? AND ts < ?
		ORDER BY bucket_ts, ts, id`, bucketSeconds, bucketSeconds, provider, authID, windowType, fromTS, toTS)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	raw := map[int64]float64{}
	for rows.Next() {
		var bucket int64
		var used float64
		if err := rows.Scan(&bucket, &used); err != nil {
			return nil, nil, err
		}
		raw[bucket] = clampPercent(used)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return interpolateQuotaUsedBuckets(raw, bucketSeconds), raw, nil
}

func quotaUsedPlateauBuckets(raw map[int64]float64) map[int64]bool {
	out := map[int64]bool{}
	if len(raw) < 2 {
		return out
	}
	buckets := make([]int64, 0, len(raw))
	for bucket := range raw {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	for i, bucket := range buckets {
		used := raw[bucket]
		if i > 0 && math.Abs(used-raw[buckets[i-1]]) <= estimatedQuotaPlateauMaxDelta {
			out[bucket] = true
			out[buckets[i-1]] = true
		}
		if i+1 < len(buckets) && math.Abs(used-raw[buckets[i+1]]) <= estimatedQuotaPlateauMaxDelta {
			out[bucket] = true
			out[buckets[i+1]] = true
		}
	}
	return out
}

func interpolateQuotaUsedBuckets(raw map[int64]float64, bucketSeconds int64) map[int64]float64 {
	out := make(map[int64]float64, len(raw))
	if len(raw) == 0 || bucketSeconds <= 0 {
		return out
	}
	buckets := make([]int64, 0, len(raw))
	for bucket, used := range raw {
		out[bucket] = used
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	for i := 0; i+1 < len(buckets); i++ {
		leftBucket := buckets[i]
		rightBucket := buckets[i+1]
		steps := (rightBucket - leftBucket) / bucketSeconds
		if steps <= 1 {
			continue
		}
		leftUsed := raw[leftBucket]
		rightUsed := raw[rightBucket]
		for step := int64(1); step < steps; step++ {
			ratio := float64(step) / float64(steps)
			out[leftBucket+step*bucketSeconds] = leftUsed + (rightUsed-leftUsed)*ratio
		}
	}
	return out
}
