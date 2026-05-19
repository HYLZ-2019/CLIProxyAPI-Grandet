package analytics

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	quotaPointsFull = 10000.0
	tokenMillion    = 1000000.0
)

type TokenDimension struct {
	Provider  string
	AuthID    string
	Model     string
	TokenType string
}

type TokenPriceRow struct {
	PriceDate             string   `json:"price_date"`
	Provider              string   `json:"provider"`
	AuthID                string   `json:"auth_id"`
	Model                 string   `json:"model"`
	TokenType             string   `json:"token_type"`
	PricePointsPerMillion *float64 `json:"price_points_per_million"`
	Status                string   `json:"status"`
	EquationCount         int      `json:"equation_count"`
	ResidualRMS           float64  `json:"residual_rms"`
	ResidualMAD           float64  `json:"residual_mad"`
	SourceFromTS          int64    `json:"source_from_ts"`
	SourceToTS            int64    `json:"source_to_ts"`
	SolvedAt              int64    `json:"solved_at"`
}

type TokenPriceSolveProviderResult struct {
	Provider       string `json:"provider"`
	AuthID         string `json:"auth_id"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	DimensionCount int    `json:"dimension_count"`
	EquationCount  int    `json:"equation_count"`
	RowCount       int    `json:"row_count"`
}

type TokenPriceSolveResponse struct {
	PriceDate string                          `json:"price_date"`
	Status    string                          `json:"status"`
	Message   string                          `json:"message"`
	Rows      []TokenPriceRow                 `json:"rows"`
	Providers []TokenPriceSolveProviderResult `json:"providers"`
}

type ProviderQuotaLinePoint struct {
	HourTS                   int64   `json:"hour_ts"`
	BucketTS                 int64   `json:"bucket_ts,omitempty"`
	BucketSeconds            int64   `json:"bucket_seconds,omitempty"`
	QuotaRemainingPoints     float64 `json:"quota_remaining_points"`
	QuotaRemainingPercent    float64 `json:"quota_remaining_percent"`
	QuotaUsedPercent         float64 `json:"quota_used_percent"`
	QuotaUsedPoints          float64 `json:"quota_used_points"`
	CLIProxyHourPoints       float64 `json:"cliproxy_hour_points"`
	CLIProxyCumulativePoints float64 `json:"cliproxy_cumulative_points"`
	QuotaEventsCount         int64   `json:"quota_events_count"`
}

type ProviderQuotaResetMarker struct {
	ResetAt int64   `json:"reset_at"`
	Points  float64 `json:"points"`
}

type ProviderQuotaSeries struct {
	Provider                           string                     `json:"provider"`
	AuthID                             string                     `json:"auth_id"`
	WindowType                         string                     `json:"window_type"`
	PriceDate                          string                     `json:"price_date"`
	MostExpensivePricePointsPerMillion float64                    `json:"most_expensive_price_points_per_million"`
	MillionTokensFor100PercentQuota    float64                    `json:"million_tokens_for_100_percent_quota"`
	Points                             []ProviderQuotaLinePoint   `json:"points"`
	ResetMarkers                       []ProviderQuotaResetMarker `json:"reset_markers"`
}

type ProviderQuotaLinesResponse struct {
	Series []ProviderQuotaSeries `json:"series"`
}

type quotaSeriesKey struct {
	provider string
	authID   string
}

type priceSolveKey struct {
	provider string
	authID   string
}

type quotaEquation struct {
	provider string
	authID   string
	fromTS   int64
	toTS     int64
	features []float64
	target   float64
}

type solveResult struct {
	coefficients []float64
	status       string
	equations    int
	rms          float64
	mad          float64
}

func (s *Store) SolveTokenPricesForDate(date time.Time) error {
	_, err := s.SolveTokenPricesForDateWithResult(date)
	return err
}

func tokenPriceSourceWindow(date time.Time) (int64, int64) {
	now := time.Now().In(date.Location())
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	if now.Year() == date.Year() && now.YearDay() == date.YearDay() {
		toTS := now.Unix()
		return toTS - 24*60*60, toTS
	}
	return dayStart.Unix(), dayStart.Add(24 * time.Hour).Unix()
}

func (s *Store) SolveTokenPricesForDateWithResult(date time.Time) (*TokenPriceSolveResponse, error) {
	priceDate := date.Format("2006-01-02")
	resp := &TokenPriceSolveResponse{PriceDate: priceDate, Status: "no_token_usage", Message: tokenPriceSolveMessage("no_token_usage")}
	if s == nil {
		resp.Rows = []TokenPriceRow{}
		return resp, nil
	}
	usedFrom, usedTo := tokenPriceSourceWindow(date)
	dims, err := s.usedTokenDimensions(usedFrom, usedTo)
	if err != nil {
		return nil, err
	}
	if len(dims) == 0 {
		rows, err := s.TokenPrices(priceDate)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []TokenPriceRow{}
		}
		resp.Rows = rows
		return resp, nil
	}
	byScope := map[priceSolveKey][]TokenDimension{}
	for _, dim := range dims {
		key := priceSolveKey{provider: dim.Provider, authID: dim.AuthID}
		byScope[key] = append(byScope[key], dim)
	}
	for key, scopeDims := range byScope {
		result, err := s.solveProviderAuthPrices(priceDate, key.provider, key.authID, scopeDims, usedTo)
		if err != nil {
			return nil, err
		}
		resp.Providers = append(resp.Providers, result)
	}
	sort.Slice(resp.Providers, func(i, j int) bool {
		if resp.Providers[i].Provider == resp.Providers[j].Provider {
			return resp.Providers[i].AuthID < resp.Providers[j].AuthID
		}
		return resp.Providers[i].Provider < resp.Providers[j].Provider
	})
	rows, err := s.TokenPrices(priceDate)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []TokenPriceRow{}
	}
	resp.Rows = rows
	resp.Status = aggregateTokenPriceSolveStatus(resp.Providers)
	resp.Message = tokenPriceSolveMessage(resp.Status)
	return resp, nil
}

func (s *Store) solveProviderAuthPrices(priceDate, provider, authID string, dims []TokenDimension, toTS int64) (TokenPriceSolveProviderResult, error) {
	result := TokenPriceSolveProviderResult{Provider: provider, AuthID: authID, Status: "no_token_usage", Message: tokenPriceSolveMessage("no_token_usage"), DimensionCount: len(dims)}
	if len(dims) == 0 {
		return result, nil
	}
	sort.Slice(dims, func(i, j int) bool {
		if dims[i].Model == dims[j].Model {
			return dims[i].TokenType < dims[j].TokenType
		}
		return dims[i].Model < dims[j].Model
	})
	fromTS := toTS - 60*86400
	windowType, err := s.chooseQuotaWindowForAuth(provider, authID, fromTS, toTS)
	if err != nil {
		return result, err
	}
	var solve solveResult
	if windowType == "" {
		solve = solveResult{status: "no_quota_snapshots"}
	} else {
		equations, err := s.buildQuotaEquationsForAuth(provider, authID, windowType, dims, fromTS, toTS)
		if err != nil {
			return result, err
		}
		solve = solveRobust(equations, len(dims))
	}
	result.Status = solve.status
	result.Message = tokenPriceSolveMessage(solve.status)
	result.EquationCount = solve.equations
	result.RowCount = len(dims)
	now := time.Now().Unix()
	for i, dim := range dims {
		var price *float64
		if solve.status == "solved" && i < len(solve.coefficients) {
			v := solve.coefficients[i]
			price = &v
		}
		row := TokenPriceRow{
			PriceDate:             priceDate,
			Provider:              dim.Provider,
			AuthID:                dim.AuthID,
			Model:                 dim.Model,
			TokenType:             dim.TokenType,
			PricePointsPerMillion: price,
			Status:                solve.status,
			EquationCount:         solve.equations,
			ResidualRMS:           solve.rms,
			ResidualMAD:           solve.mad,
			SourceFromTS:          fromTS,
			SourceToTS:            toTS,
			SolvedAt:              now,
		}
		if err := s.UpsertTokenPrice(row); err != nil {
			return result, err
		}
	}
	return result, nil
}

func aggregateTokenPriceSolveStatus(providers []TokenPriceSolveProviderResult) string {
	if len(providers) == 0 {
		return "no_token_usage"
	}
	solved := 0
	counts := map[string]int{}
	for _, p := range providers {
		if p.Status == "solved" {
			solved++
		}
		counts[p.Status]++
	}
	if solved == len(providers) {
		return "solved"
	}
	if solved > 0 {
		return "partial"
	}
	for _, status := range []string{"no_quota_snapshots", "insufficient_equations", "rank_deficient", "low_confidence", "no_token_usage"} {
		if counts[status] > 0 {
			return status
		}
	}
	return "insufficient_equations"
}

func tokenPriceSolveMessage(status string) string {
	switch status {
	case "solved":
		return "Token prices solved."
	case "partial":
		return "Some providers were solved, but others need more data."
	case "no_token_usage":
		return "No token usage was found for the source window."
	case "no_quota_snapshots":
		return "No quota snapshots were found for the source window."
	case "rank_deficient":
		return "The available data points are not diverse enough to solve prices."
	case "low_confidence":
		return "The solve result has low confidence."
	default:
		return "Not enough data points to solve token prices."
	}
}

func (s *Store) usedTokenDimensions(fromTS, toTS int64) ([]TokenDimension, error) {
	dims := map[TokenDimension]struct{}{}
	queries := []string{
		`SELECT provider, auth_id, model,
		        COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cached_tokens), 0)
		 FROM query_logs
		 WHERE ts >= ? AND ts < ? AND provider <> '' AND model <> ''
		 GROUP BY provider, auth_id, model`,
		`SELECT provider, auth_id, model,
		        COALESCE(SUM(input_tokens_sum), 0), COALESCE(SUM(output_tokens_sum), 0), COALESCE(SUM(cached_tokens_sum), 0)
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
			var input, output, cached int64
			if err := rows.Scan(&provider, &authID, &model, &input, &output, &cached); err != nil {
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

func (s *Store) chooseQuotaWindow(provider string, fromTS, toTS int64) (string, error) {
	rows, err := s.db.Query(`
		SELECT window_type, MAX(ts) FROM quota_snapshots
		WHERE provider = ? AND ts >= ? AND ts < ?
		GROUP BY window_type`, provider, fromTS, toTS)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	return scanPreferredQuotaWindow(rows, "")
}

func (s *Store) chooseQuotaWindowForAuth(provider, authID string, fromTS, toTS int64) (string, error) {
	return s.chooseQuotaWindowForAuthClass(provider, authID, fromTS, toTS, "")
}

// chooseQuotaWindowForAuthClass picks a window_type to use for an (provider, auth).
// When windowClass is "5h" or "7d", windows that classify to that class are preferred;
// if none match, it falls back to the unconstrained preference order.
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
		for _, e := range all {
			if classifyQuotaWindow(e.windowType) == windowClass {
				matched = append(matched, e)
			}
		}
		if len(matched) > 0 {
			return pick(matched), nil
		}
	}
	return pick(all), nil
}

// classifyQuotaWindow groups a window_type into "5h" (five-hour limits) or "7d"
// (seven-day / weekly limits). Returns "" for provider-specific windows that don't
// fit either bucket (e.g. Gemini-CLI's per-model "<model>:REQUESTS" windows).
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

func (s *Store) buildQuotaEquationsForAuth(provider, authID, windowType string, dims []TokenDimension, fromTS, toTS int64) ([]quotaEquation, error) {
	if windowType == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT ts, used_percent
		FROM quota_snapshots
		WHERE provider = ? AND auth_id = ? AND window_type = ? AND ts >= ? AND ts < ?
		ORDER BY ts`, provider, authID, windowType, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type snap struct {
		ts   int64
		used float64
	}
	var snaps []snap
	for rows.Next() {
		var srow snap
		if err := rows.Scan(&srow.ts, &srow.used); err != nil {
			return nil, err
		}
		snaps = append(snaps, srow)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var equations []quotaEquation
	for i := 0; i+1 < len(snaps); i++ {
		a := normalizeUsedFraction(snaps[i].used)
		b := normalizeUsedFraction(snaps[i+1].used)
		if b <= a {
			continue
		}
		startTS := snaps[i].ts
		endTS := snaps[i+1].ts
		if endTS <= startTS {
			continue
		}
		features, err := s.intervalFeatures(provider, authID, startTS, endTS, dims)
		if err != nil {
			return nil, err
		}
		if sumFloat(features) == 0 {
			continue
		}
		equations = append(equations, quotaEquation{
			provider: provider,
			authID:   authID,
			fromTS:   startTS,
			toTS:     endTS,
			features: features,
			target:   (b - a) * quotaPointsFull,
		})
	}
	sort.Slice(equations, func(i, j int) bool { return equations[i].fromTS > equations[j].fromTS })
	if len(equations) > 500 {
		equations = equations[:500]
	}
	return equations, nil
}

func (s *Store) intervalFeatures(provider, authID string, fromTS, toTS int64, dims []TokenDimension) ([]float64, error) {
	features, rowCount, err := s.queryLogIntervalFeaturesForAuth(provider, authID, fromTS, toTS, dims)
	if err != nil || rowCount > 0 || authID == "" {
		return features, err
	}
	features, rowCount, err = s.queryLogIntervalFeaturesForAuth(provider, "", fromTS, toTS, dims)
	if err != nil || rowCount > 0 {
		return features, err
	}
	if s.rawLogsMayCover(fromTS) {
		return features, nil
	}
	features, err = s.hourlyIntervalFeaturesForAuth(provider, authID, fromTS, toTS, dims)
	if err != nil || sumFloat(features) > 0 || authID == "" {
		return features, err
	}
	return s.hourlyIntervalFeaturesForAuth(provider, "", fromTS, toTS, dims)
}

func (s *Store) queryLogIntervalFeaturesForAuth(provider, authID string, fromTS, toTS int64, dims []TokenDimension) ([]float64, int64, error) {
	var rows *sql.Rows
	var err error
	if authID == "" {
		rows, err = s.db.Query(`
			SELECT model, SUM(input_tokens), SUM(output_tokens), SUM(cached_tokens), COUNT(*)
			FROM query_logs
			WHERE provider = ? AND ts >= ? AND ts < ?
			GROUP BY model`, provider, fromTS, toTS)
	} else {
		rows, err = s.db.Query(`
			SELECT model, SUM(input_tokens), SUM(output_tokens), SUM(cached_tokens), COUNT(*)
			FROM query_logs
			WHERE provider = ? AND auth_id = ? AND ts >= ? AND ts < ?
			GROUP BY model`, provider, authID, fromTS, toTS)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	sums := map[string]tokenFeatureSums{}
	var rowCount int64
	for rows.Next() {
		var model string
		var input, output, cached, count int64
		if err := rows.Scan(&model, &input, &output, &cached, &count); err != nil {
			return nil, 0, err
		}
		sums[model] = tokenFeatureSums{input: float64(input), output: float64(output), cached: float64(cached)}
		rowCount += count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return tokenFeaturesFromSums(sums, dims), rowCount, nil
}

func (s *Store) hourlyIntervalFeaturesForAuth(provider, authID string, fromTS, toTS int64, dims []TokenDimension) ([]float64, error) {
	var rows *sql.Rows
	var err error
	if authID == "" {
		rows, err = s.db.Query(`
			SELECT hour_ts, model, SUM(input_tokens_sum), SUM(output_tokens_sum), SUM(cached_tokens_sum)
			FROM hourly_aggregates
			WHERE provider = ? AND hour_ts < ? AND hour_ts + 3600 > ?
			GROUP BY hour_ts, model`, provider, toTS, fromTS)
	} else {
		rows, err = s.db.Query(`
			SELECT hour_ts, model, SUM(input_tokens_sum), SUM(output_tokens_sum), SUM(cached_tokens_sum)
			FROM hourly_aggregates
			WHERE provider = ? AND auth_id = ? AND hour_ts < ? AND hour_ts + 3600 > ?
			GROUP BY hour_ts, model`, provider, authID, toTS, fromTS)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sums := map[string]tokenFeatureSums{}
	for rows.Next() {
		var hourTS int64
		var model string
		var input, output, cached int64
		if err := rows.Scan(&hourTS, &model, &input, &output, &cached); err != nil {
			return nil, err
		}
		overlapStart := maxInt64(fromTS, hourTS)
		overlapEnd := minInt64(toTS, hourTS+analyticsHourBucketSeconds)
		if overlapEnd <= overlapStart {
			continue
		}
		factor := float64(overlapEnd-overlapStart) / float64(analyticsHourBucketSeconds)
		cur := sums[model]
		cur.input += float64(input) * factor
		cur.output += float64(output) * factor
		cur.cached += float64(cached) * factor
		sums[model] = cur
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tokenFeaturesFromSums(sums, dims), nil
}

func (s *Store) rawLogsMayCover(fromTS int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	retentionSeconds := s.rawRetentionSeconds
	s.mu.RUnlock()
	return retentionSeconds > 0 && fromTS >= time.Now().Unix()-retentionSeconds
}

type tokenFeatureSums struct {
	input  float64
	output float64
	cached float64
}

func tokenFeaturesFromSums(sums map[string]tokenFeatureSums, dims []TokenDimension) []float64 {
	features := make([]float64, len(dims))
	for i, dim := range dims {
		vals := sums[dim.Model]
		switch dim.TokenType {
		case "input":
			features[i] = vals.input / tokenMillion
		case "output":
			features[i] = vals.output / tokenMillion
		case "cached_input":
			features[i] = vals.cached / tokenMillion
		}
	}
	return features
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Store) UpsertTokenPrice(row TokenPriceRow) error {
	var price any
	if row.PricePointsPerMillion != nil {
		price = *row.PricePointsPerMillion
	}
	_, err := s.db.Exec(`
		INSERT INTO daily_token_prices
		(price_date,provider,auth_id,model,token_type,price_points_per_million,status,equation_count,
		 residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(price_date,provider,auth_id,model,token_type) DO UPDATE SET
		 price_points_per_million=excluded.price_points_per_million,
		 status=excluded.status,
		 equation_count=excluded.equation_count,
		 residual_rms=excluded.residual_rms,
		 residual_mad=excluded.residual_mad,
		 source_from_ts=excluded.source_from_ts,
		 source_to_ts=excluded.source_to_ts,
		 solved_at=excluded.solved_at`,
		row.PriceDate, row.Provider, row.AuthID, row.Model, row.TokenType, price, row.Status, row.EquationCount,
		row.ResidualRMS, row.ResidualMAD, row.SourceFromTS, row.SourceToTS, row.SolvedAt)
	return err
}

func (s *Store) TokenPrices(priceDate string) ([]TokenPriceRow, error) {
	if s == nil {
		return nil, nil
	}
	if strings.TrimSpace(priceDate) == "" {
		priceDate = time.Now().Format("2006-01-02")
	}
	rows, err := s.db.Query(`
		SELECT price_date,provider,auth_id,model,token_type,price_points_per_million,status,equation_count,
		       residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at
		FROM daily_token_prices
		WHERE price_date = ?
		ORDER BY provider, auth_id, model, token_type`, priceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenPriceRow
	for rows.Next() {
		var r TokenPriceRow
		var price sql.NullFloat64
		if err := rows.Scan(&r.PriceDate, &r.Provider, &r.AuthID, &r.Model, &r.TokenType, &price, &r.Status,
			&r.EquationCount, &r.ResidualRMS, &r.ResidualMAD, &r.SourceFromTS, &r.SourceToTS, &r.SolvedAt); err != nil {
			return nil, err
		}
		if price.Valid {
			r.PricePointsPerMillion = &price.Float64
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) ProviderQuotaLines(fromTS, toTS int64, resetOn429, resetOnRefresh bool, windowClass string) (*ProviderQuotaLinesResponse, error) {
	if s == nil {
		return &ProviderQuotaLinesResponse{}, nil
	}
	keys, err := s.analyticsAuthRefs(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	priceDate := time.Now().Format("2006-01-02")
	priceRows, err := s.latestSolvedPrices(priceDate)
	if err != nil {
		return nil, err
	}
	bucketSeconds := analyticsBucketSeconds(fromTS, toTS)
	bucketPoints, err := s.pricePointsByAuthBucket(fromTS, toTS, bucketSeconds, priceRows)
	if err != nil {
		return nil, err
	}
	events, err := s.eventCountsByAuthBucket(fromTS, toTS, bucketSeconds)
	if err != nil {
		return nil, err
	}
	resetMarkers, err := s.resetMarkersByAuth(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	resp := &ProviderQuotaLinesResponse{}
	for _, key := range keys {
		windowType, err := s.chooseQuotaWindowForAuthClass(key.provider, key.authID, fromTS-60*86400, toTS, windowClass)
		if err != nil {
			return nil, err
		}
		usedByBucket, err := s.quotaUsedByBucket(key.provider, key.authID, windowType, fromTS, toTS, bucketSeconds)
		if err != nil {
			return nil, err
		}
		series := ProviderQuotaSeries{Provider: key.provider, AuthID: key.authID, WindowType: windowType, PriceDate: priceDate}
		series.MostExpensivePricePointsPerMillion = maxProviderAuthPrice(key.provider, key.authID, priceRows)
		if series.MostExpensivePricePointsPerMillion > 0 {
			series.MillionTokensFor100PercentQuota = quotaPointsFull / series.MostExpensivePricePointsPerMillion
		}
		for _, resetAt := range resetMarkers[key] {
			series.ResetMarkers = append(series.ResetMarkers, ProviderQuotaResetMarker{ResetAt: resetAt, Points: 0})
		}
		var cumulative, lastUsed float64
		var haveLast bool
		for bucket := floorBucket(fromTS, bucketSeconds); bucket < toTS; bucket += bucketSeconds {
			used, ok := usedByBucket[bucket]
			if ok && haveLast && used < lastUsed {
				cumulative = 0
			}
			if ok {
				lastUsed = used
				haveLast = true
			} else if haveLast {
				used = lastUsed
			}
			eventCount := events[key][bucket]
			if resetOn429 && eventCount > 0 {
				cumulative = 0
			}
			if resetOnRefresh && hasResetInBucket(resetMarkers[key], bucket, bucketSeconds) {
				cumulative = 0
			}
			bucketValue := bucketPoints[key][bucket]
			cumulative += bucketValue
			remainingPercent := math.Max(0, 100-used)
			usedPercent := math.Max(0, used)
			series.Points = append(series.Points, ProviderQuotaLinePoint{
				HourTS:                   bucket,
				BucketTS:                 bucket,
				BucketSeconds:            bucketSeconds,
				QuotaRemainingPoints:     remainingPercent / 100 * quotaPointsFull,
				QuotaRemainingPercent:    remainingPercent,
				QuotaUsedPercent:         used,
				QuotaUsedPoints:          usedPercent / 100 * quotaPointsFull,
				CLIProxyHourPoints:       bucketValue,
				CLIProxyCumulativePoints: cumulative,
				QuotaEventsCount:         eventCount,
			})
		}
		resp.Series = append(resp.Series, series)
	}
	return resp, nil
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

func (s *Store) latestSolvedPrices(priceDate string) (map[TokenDimension]float64, error) {
	rows, err := s.db.Query(`
		SELECT provider,auth_id,model,token_type,price_points_per_million
		FROM daily_token_prices
		WHERE status = 'solved' AND price_points_per_million IS NOT NULL
		  AND price_date = (
		    SELECT MAX(price_date) FROM daily_token_prices d2
		    WHERE d2.provider = daily_token_prices.provider
		      AND d2.auth_id = daily_token_prices.auth_id
		      AND d2.model = daily_token_prices.model
		      AND d2.token_type = daily_token_prices.token_type
		      AND d2.status = 'solved'
		      AND d2.price_points_per_million IS NOT NULL
		      AND d2.price_date <= ?
		  )`, priceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prices := map[TokenDimension]float64{}
	for rows.Next() {
		var dim TokenDimension
		var price float64
		if err := rows.Scan(&dim.Provider, &dim.AuthID, &dim.Model, &dim.TokenType, &price); err != nil {
			return nil, err
		}
		prices[dim] = price
	}
	return prices, nil
}

func (s *Store) pricePointsByAuthBucket(fromTS, toTS, bucketSeconds int64, prices map[TokenDimension]float64) (map[quotaSeriesKey]map[int64]float64, error) {
	if bucketSeconds == analyticsFiveMinuteBucketSeconds {
		return s.queryLogBucketPricePoints(fromTS, toTS, bucketSeconds, prices)
	}
	return s.hourlyBucketPricePoints(fromTS, toTS, bucketSeconds, prices)
}

func (s *Store) queryLogBucketPricePoints(fromTS, toTS, bucketSeconds int64, prices map[TokenDimension]float64) (map[quotaSeriesKey]map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket_ts,provider,auth_id,model,
		       SUM(input_tokens),SUM(output_tokens),SUM(cached_tokens)
		FROM query_logs
		WHERE ts >= ? AND ts < ?
		GROUP BY bucket_ts,provider,auth_id,model`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPricePointRows(rows, prices)
}

func (s *Store) hourlyBucketPricePoints(fromTS, toTS, bucketSeconds int64, prices map[TokenDimension]float64) (map[quotaSeriesKey]map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (hour_ts / ?) * ? AS bucket_ts,provider,auth_id,model,
		       SUM(input_tokens_sum),SUM(output_tokens_sum),SUM(cached_tokens_sum)
		FROM hourly_aggregates
		WHERE hour_ts >= ? AND hour_ts < ?
		GROUP BY bucket_ts,provider,auth_id,model`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPricePointRows(rows, prices)
}

func scanPricePointRows(rows *sql.Rows, prices map[TokenDimension]float64) (map[quotaSeriesKey]map[int64]float64, error) {
	out := map[quotaSeriesKey]map[int64]float64{}
	for rows.Next() {
		var bucket int64
		var key quotaSeriesKey
		var model string
		var input, output, cached int64
		if err := rows.Scan(&bucket, &key.provider, &key.authID, &model, &input, &output, &cached); err != nil {
			return nil, err
		}
		points := float64(input)/tokenMillion*prices[TokenDimension{Provider: key.provider, AuthID: key.authID, Model: model, TokenType: "input"}] +
			float64(output)/tokenMillion*prices[TokenDimension{Provider: key.provider, AuthID: key.authID, Model: model, TokenType: "output"}] +
			float64(cached)/tokenMillion*prices[TokenDimension{Provider: key.provider, AuthID: key.authID, Model: model, TokenType: "cached_input"}]
		if out[key] == nil {
			out[key] = map[int64]float64{}
		}
		out[key][bucket] += points
	}
	return out, rows.Err()
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

func (s *Store) resetMarkersByAuth(fromTS, toTS int64) (map[quotaSeriesKey][]int64, error) {
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

func hasResetInBucket(resetTimes []int64, bucketTS, bucketSeconds int64) bool {
	for _, resetAt := range resetTimes {
		if floorBucket(resetAt, bucketSeconds) == bucketTS {
			return true
		}
	}
	return false
}

func (s *Store) quotaUsedByBucket(provider, authID, windowType string, fromTS, toTS, bucketSeconds int64) (map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket_ts, used_percent
		FROM quota_snapshots
		WHERE provider = ? AND auth_id = ? AND window_type = ? AND ts >= ? AND ts < ?
		ORDER BY bucket_ts, ts, id`, bucketSeconds, bucketSeconds, provider, authID, windowType, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var bucket int64
		var used float64
		if err := rows.Scan(&bucket, &used); err != nil {
			return nil, err
		}
		out[bucket] = normalizeUsedFraction(used) * 100
	}
	return out, rows.Err()
}

func solveRobust(equations []quotaEquation, dimensions int) solveResult {
	if dimensions == 0 {
		return solveResult{status: "insufficient_equations"}
	}
	if len(equations) < dimensions {
		return solveResult{status: "insufficient_equations", equations: len(equations)}
	}
	active := make([]quotaEquation, len(equations))
	copy(active, equations)
	var coeff []float64
	var residuals []float64
	status := "solved"
	for iter := 0; iter < 4; iter++ {
		var rank int
		var ok bool
		coeff, rank, ok = solveLeastSquares(active, dimensions)
		if !ok || rank < dimensions {
			return solveResult{status: "rank_deficient", equations: len(active)}
		}
		residuals = equationResiduals(active, coeff)
		if iter == 3 || len(active) <= dimensions+2 {
			break
		}
		mad := medianAbsDeviation(residuals)
		threshold := math.Max(3*mad, 100)
		filtered := active[:0]
		removed := 0
		maxRemove := int(math.Ceil(float64(len(active)) * 0.2))
		for i, eq := range active {
			if math.Abs(residuals[i]) > threshold && removed < maxRemove {
				removed++
				continue
			}
			filtered = append(filtered, eq)
		}
		if removed == 0 {
			break
		}
		active = append([]quotaEquation(nil), filtered...)
	}
	for _, v := range coeff {
		if !isFinite(v) || v < 0 {
			status = "low_confidence"
			break
		}
	}
	rms := residualRMS(residuals)
	mad := medianAbsDeviation(residuals)
	if status == "solved" && rms > 2500 {
		status = "low_confidence"
	}
	if status != "solved" {
		coeff = nil
	}
	return solveResult{coefficients: coeff, status: status, equations: len(active), rms: rms, mad: mad}
}

func solveLeastSquares(equations []quotaEquation, dimensions int) ([]float64, int, bool) {
	normal := make([][]float64, dimensions)
	rhs := make([]float64, dimensions)
	for i := range normal {
		normal[i] = make([]float64, dimensions)
	}
	for _, eq := range equations {
		for i := 0; i < dimensions; i++ {
			rhs[i] += eq.features[i] * eq.target
			for j := 0; j < dimensions; j++ {
				normal[i][j] += eq.features[i] * eq.features[j]
			}
		}
	}
	for i := 0; i < dimensions; i++ {
		normal[i][i] += 1e-8
	}
	return solveLinear(normal, rhs)
}

func solveLinear(a [][]float64, b []float64) ([]float64, int, bool) {
	n := len(b)
	rank := 0
	for col := 0; col < n; col++ {
		pivot := col
		for row := col + 1; row < n; row++ {
			if math.Abs(a[row][col]) > math.Abs(a[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(a[pivot][col]) < 1e-12 {
			continue
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]
		rank++
		pivotValue := a[col][col]
		for j := col; j < n; j++ {
			a[col][j] /= pivotValue
		}
		b[col] /= pivotValue
		for row := 0; row < n; row++ {
			if row == col {
				continue
			}
			factor := a[row][col]
			for j := col; j < n; j++ {
				a[row][j] -= factor * a[col][j]
			}
			b[row] -= factor * b[col]
		}
	}
	if rank < n {
		return nil, rank, false
	}
	return b, rank, true
}

func equationResiduals(equations []quotaEquation, coeff []float64) []float64 {
	out := make([]float64, len(equations))
	for i, eq := range equations {
		out[i] = dot(eq.features, coeff) - eq.target
	}
	return out
}

func residualRMS(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(values)))
}

func medianAbsDeviation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	med := median(values)
	abs := make([]float64, len(values))
	for i, v := range values {
		abs[i] = math.Abs(v - med)
	}
	return median(abs)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[mid]
	}
	return (copyValues[mid-1] + copyValues[mid]) / 2
}

func normalizeUsedFraction(v float64) float64 {
	if v > 1 {
		return v / 100
	}
	return v
}

func sumFloat(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum
}

func dot(a, b []float64) float64 {
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func maxProviderAuthPrice(provider, authID string, prices map[TokenDimension]float64) float64 {
	var maxPrice float64
	for dim, price := range prices {
		if dim.Provider == provider && dim.AuthID == authID && price > maxPrice {
			maxPrice = price
		}
	}
	return maxPrice
}

func DebugSolveTokenPrices(store *Store, date time.Time) error {
	if store == nil {
		return fmt.Errorf("analytics store is nil")
	}
	return store.SolveTokenPricesForDate(date)
}
