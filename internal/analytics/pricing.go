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
	Model     string
	TokenType string
}

type TokenPriceRow struct {
	PriceDate             string   `json:"price_date"`
	Provider              string   `json:"provider"`
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

type ProviderQuotaLinePoint struct {
	HourTS                   int64   `json:"hour_ts"`
	QuotaRemainingPoints     float64 `json:"quota_remaining_points"`
	QuotaRemainingPercent    float64 `json:"quota_remaining_percent"`
	QuotaUsedPercent         float64 `json:"quota_used_percent"`
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

type quotaEquation struct {
	provider string
	authID   string
	hourTS   int64
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
	if s == nil {
		return nil
	}
	priceDate := date.Format("2006-01-02")
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	usedFrom := dayStart.Add(-24 * time.Hour).Unix()
	usedTo := dayStart.Unix()
	if usedTo <= 0 {
		usedTo = time.Now().Unix()
	}
	dims, err := s.usedTokenDimensions(usedFrom, usedTo)
	if err != nil {
		return err
	}
	byProvider := map[string][]TokenDimension{}
	for _, dim := range dims {
		byProvider[dim.Provider] = append(byProvider[dim.Provider], dim)
	}
	for provider, providerDims := range byProvider {
		if err := s.solveProviderPrices(priceDate, provider, providerDims, usedTo); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) solveProviderPrices(priceDate, provider string, dims []TokenDimension, toTS int64) error {
	if len(dims) == 0 {
		return nil
	}
	sort.Slice(dims, func(i, j int) bool {
		if dims[i].Model == dims[j].Model {
			return dims[i].TokenType < dims[j].TokenType
		}
		return dims[i].Model < dims[j].Model
	})
	fromTS := toTS - 60*86400
	windowType, err := s.chooseQuotaWindow(provider, fromTS, toTS)
	if err != nil {
		return err
	}
	equations, err := s.buildQuotaEquations(provider, windowType, dims, fromTS, toTS)
	if err != nil {
		return err
	}
	result := solveRobust(equations, len(dims))
	now := time.Now().Unix()
	for i, dim := range dims {
		var price *float64
		if result.status == "solved" && i < len(result.coefficients) {
			v := result.coefficients[i]
			price = &v
		}
		row := TokenPriceRow{
			PriceDate:             priceDate,
			Provider:              dim.Provider,
			Model:                 dim.Model,
			TokenType:             dim.TokenType,
			PricePointsPerMillion: price,
			Status:                result.status,
			EquationCount:         result.equations,
			ResidualRMS:           result.rms,
			ResidualMAD:           result.mad,
			SourceFromTS:          fromTS,
			SourceToTS:            toTS,
			SolvedAt:              now,
		}
		if err := s.UpsertTokenPrice(row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) usedTokenDimensions(fromTS, toTS int64) ([]TokenDimension, error) {
	rows, err := s.db.Query(`
		SELECT provider, model,
		       SUM(input_tokens_sum), SUM(output_tokens_sum), SUM(cached_tokens_sum)
		FROM hourly_aggregates
		WHERE hour_ts >= ? AND hour_ts < ? AND provider <> '' AND model <> ''
		GROUP BY provider, model`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dims []TokenDimension
	for rows.Next() {
		var provider, model string
		var input, output, cached int64
		if err := rows.Scan(&provider, &model, &input, &output, &cached); err != nil {
			return nil, err
		}
		if input > 0 {
			dims = append(dims, TokenDimension{Provider: provider, Model: model, TokenType: "input"})
		}
		if output > 0 {
			dims = append(dims, TokenDimension{Provider: provider, Model: model, TokenType: "output"})
		}
		if cached > 0 {
			dims = append(dims, TokenDimension{Provider: provider, Model: model, TokenType: "cached_input"})
		}
	}
	return dims, nil
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
	latest := ""
	var latestTS int64
	for rows.Next() {
		var wt string
		var ts int64
		if err := rows.Scan(&wt, &ts); err != nil {
			return "", err
		}
		w := strings.TrimSpace(wt)
		if strings.EqualFold(w, "weekly") {
			return wt, nil
		}
		if strings.EqualFold(w, "default") {
			latest = wt
			latestTS = ts
			continue
		}
		if latest == "" || ts > latestTS {
			latest = wt
			latestTS = ts
		}
	}
	return latest, nil
}

func (s *Store) buildQuotaEquations(provider, windowType string, dims []TokenDimension, fromTS, toTS int64) ([]quotaEquation, error) {
	if windowType == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT ts, auth_id, used_percent
		FROM quota_snapshots
		WHERE provider = ? AND window_type = ? AND ts >= ? AND ts < ?
		ORDER BY auth_id, ts`, provider, windowType, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type snap struct {
		ts     int64
		authID string
		used   float64
	}
	byAuth := map[string][]snap{}
	for rows.Next() {
		var srow snap
		if err := rows.Scan(&srow.ts, &srow.authID, &srow.used); err != nil {
			return nil, err
		}
		byAuth[srow.authID] = append(byAuth[srow.authID], srow)
	}
	var equations []quotaEquation
	for authID, snaps := range byAuth {
		for i := 0; i+1 < len(snaps); i++ {
			a := normalizeUsedFraction(snaps[i].used)
			b := normalizeUsedFraction(snaps[i+1].used)
			if b <= a {
				continue
			}
			hourTS := snaps[i].ts - snaps[i].ts%3600
			features, err := s.hourlyFeatures(provider, authID, hourTS, dims)
			if err != nil {
				return nil, err
			}
			if sumFloat(features) == 0 {
				continue
			}
			equations = append(equations, quotaEquation{
				provider: provider,
				authID:   authID,
				hourTS:   hourTS,
				features: features,
				target:   (b - a) * quotaPointsFull,
			})
		}
	}
	sort.Slice(equations, func(i, j int) bool { return equations[i].hourTS > equations[j].hourTS })
	if len(equations) > 500 {
		equations = equations[:500]
	}
	return equations, nil
}

func (s *Store) hourlyFeatures(provider, authID string, hourTS int64, dims []TokenDimension) ([]float64, error) {
	features, err := s.hourlyFeaturesForAuth(provider, authID, hourTS, dims)
	if err != nil || sumFloat(features) > 0 || authID == "" {
		return features, err
	}
	return s.hourlyFeaturesForAuth(provider, "", hourTS, dims)
}

func (s *Store) hourlyFeaturesForAuth(provider, authID string, hourTS int64, dims []TokenDimension) ([]float64, error) {
	features := make([]float64, len(dims))
	var rows *sql.Rows
	var err error
	if authID == "" {
		rows, err = s.db.Query(`
			SELECT model, SUM(input_tokens_sum), SUM(output_tokens_sum), SUM(cached_tokens_sum)
			FROM hourly_aggregates
			WHERE provider = ? AND hour_ts = ?
			GROUP BY model`, provider, hourTS)
	} else {
		rows, err = s.db.Query(`
			SELECT model, SUM(input_tokens_sum), SUM(output_tokens_sum), SUM(cached_tokens_sum)
			FROM hourly_aggregates
			WHERE provider = ? AND auth_id = ? AND hour_ts = ?
			GROUP BY model`, provider, authID, hourTS)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byModel := map[string][3]int64{}
	for rows.Next() {
		var model string
		var input, output, cached int64
		if err := rows.Scan(&model, &input, &output, &cached); err != nil {
			return nil, err
		}
		byModel[model] = [3]int64{input, output, cached}
	}
	for i, dim := range dims {
		vals := byModel[dim.Model]
		switch dim.TokenType {
		case "input":
			features[i] = float64(vals[0]) / tokenMillion
		case "output":
			features[i] = float64(vals[1]) / tokenMillion
		case "cached_input":
			features[i] = float64(vals[2]) / tokenMillion
		}
	}
	return features, nil
}

func (s *Store) UpsertTokenPrice(row TokenPriceRow) error {
	var price any
	if row.PricePointsPerMillion != nil {
		price = *row.PricePointsPerMillion
	}
	_, err := s.db.Exec(`
		INSERT INTO daily_token_prices
		(price_date,provider,model,token_type,price_points_per_million,status,equation_count,
		 residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(price_date,provider,model,token_type) DO UPDATE SET
		 price_points_per_million=excluded.price_points_per_million,
		 status=excluded.status,
		 equation_count=excluded.equation_count,
		 residual_rms=excluded.residual_rms,
		 residual_mad=excluded.residual_mad,
		 source_from_ts=excluded.source_from_ts,
		 source_to_ts=excluded.source_to_ts,
		 solved_at=excluded.solved_at`,
		row.PriceDate, row.Provider, row.Model, row.TokenType, price, row.Status, row.EquationCount,
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
		SELECT price_date,provider,model,token_type,price_points_per_million,status,equation_count,
		       residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at
		FROM daily_token_prices
		WHERE price_date = ?
		ORDER BY provider, model, token_type`, priceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenPriceRow
	for rows.Next() {
		var r TokenPriceRow
		var price sql.NullFloat64
		if err := rows.Scan(&r.PriceDate, &r.Provider, &r.Model, &r.TokenType, &price, &r.Status,
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

func (s *Store) ProviderQuotaLines(fromTS, toTS int64, resetOn429, resetOnRefresh bool) (*ProviderQuotaLinesResponse, error) {
	if s == nil {
		return &ProviderQuotaLinesResponse{}, nil
	}
	providers, err := s.analyticsProviders(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	priceDate := time.Now().Format("2006-01-02")
	priceRows, err := s.latestSolvedPrices(priceDate)
	if err != nil {
		return nil, err
	}
	hourPoints, err := s.hourlyPricePoints(fromTS, toTS, priceRows)
	if err != nil {
		return nil, err
	}
	events, err := s.eventCountsByProviderHour(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	resetMarkers, err := s.resetMarkersByProvider(fromTS, toTS)
	if err != nil {
		return nil, err
	}
	resp := &ProviderQuotaLinesResponse{}
	for _, provider := range providers {
		windowType, err := s.chooseQuotaWindow(provider, fromTS-60*86400, toTS)
		if err != nil {
			return nil, err
		}
		usedByHour, err := s.quotaUsedByHour(provider, windowType, fromTS, toTS)
		if err != nil {
			return nil, err
		}
		series := ProviderQuotaSeries{Provider: provider, WindowType: windowType, PriceDate: priceDate}
		series.MostExpensivePricePointsPerMillion = maxProviderPrice(provider, priceRows)
		if series.MostExpensivePricePointsPerMillion > 0 {
			series.MillionTokensFor100PercentQuota = quotaPointsFull / series.MostExpensivePricePointsPerMillion
		}
		for _, resetAt := range resetMarkers[provider] {
			series.ResetMarkers = append(series.ResetMarkers, ProviderQuotaResetMarker{ResetAt: resetAt, Points: 0})
		}
		var cumulative, lastUsed float64
		var haveLast bool
		for hour := floorHour(fromTS); hour < toTS; hour += 3600 {
			used, ok := usedByHour[hour]
			if ok && haveLast && used < lastUsed {
				cumulative = 0
			}
			if ok {
				lastUsed = used
				haveLast = true
			} else if haveLast {
				used = lastUsed
			}
			eventCount := events[provider][hour]
			if resetOn429 && eventCount > 0 {
				cumulative = 0
			}
			if resetOnRefresh && hasResetInHour(resetMarkers[provider], hour) {
				cumulative = 0
			}
			hourValue := hourPoints[provider][hour]
			cumulative += hourValue
			remainingPercent := math.Max(0, 100-used)
			series.Points = append(series.Points, ProviderQuotaLinePoint{
				HourTS:                   hour,
				QuotaRemainingPoints:     remainingPercent / 100 * quotaPointsFull,
				QuotaRemainingPercent:    remainingPercent,
				QuotaUsedPercent:         used,
				CLIProxyHourPoints:       hourValue,
				CLIProxyCumulativePoints: cumulative,
				QuotaEventsCount:         eventCount,
			})
		}
		resp.Series = append(resp.Series, series)
	}
	return resp, nil
}

func (s *Store) analyticsProviders(fromTS, toTS int64) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT provider FROM hourly_aggregates WHERE hour_ts >= ? AND hour_ts < ? AND provider <> ''
		UNION
		SELECT provider FROM quota_snapshots WHERE ts >= ? AND ts < ? AND provider <> ''
		UNION
		SELECT provider FROM quota_exhaustion_events WHERE ((ts >= ? AND ts < ?) OR (reset_at >= ? AND reset_at < ?)) AND provider <> ''
		ORDER BY provider`, fromTS, toTS, fromTS, toTS, fromTS, toTS, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}

func (s *Store) latestSolvedPrices(priceDate string) (map[TokenDimension]float64, error) {
	rows, err := s.db.Query(`
		SELECT provider,model,token_type,price_points_per_million
		FROM daily_token_prices
		WHERE status = 'solved' AND price_points_per_million IS NOT NULL
		  AND price_date = (
		    SELECT MAX(price_date) FROM daily_token_prices d2
		    WHERE d2.provider = daily_token_prices.provider
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
		if err := rows.Scan(&dim.Provider, &dim.Model, &dim.TokenType, &price); err != nil {
			return nil, err
		}
		prices[dim] = price
	}
	return prices, nil
}

func (s *Store) hourlyPricePoints(fromTS, toTS int64, prices map[TokenDimension]float64) (map[string]map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT hour_ts,provider,model,
		       SUM(input_tokens_sum),SUM(output_tokens_sum),SUM(cached_tokens_sum)
		FROM hourly_aggregates
		WHERE hour_ts >= ? AND hour_ts < ?
		GROUP BY hour_ts,provider,model`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int64]float64{}
	for rows.Next() {
		var hour int64
		var provider, model string
		var input, output, cached int64
		if err := rows.Scan(&hour, &provider, &model, &input, &output, &cached); err != nil {
			return nil, err
		}
		points := float64(input)/tokenMillion*prices[TokenDimension{provider, model, "input"}] +
			float64(output)/tokenMillion*prices[TokenDimension{provider, model, "output"}] +
			float64(cached)/tokenMillion*prices[TokenDimension{provider, model, "cached_input"}]
		if out[provider] == nil {
			out[provider] = map[int64]float64{}
		}
		out[provider][hour] += points
	}
	return out, nil
}

func (s *Store) eventCountsByProviderHour(fromTS, toTS int64) (map[string]map[int64]int64, error) {
	rows, err := s.db.Query(`
		SELECT provider, (ts / 3600) * 3600 AS hour_ts, COUNT(*)
		FROM quota_exhaustion_events
		WHERE ts >= ? AND ts < ?
		GROUP BY provider, hour_ts`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int64]int64{}
	for rows.Next() {
		var provider string
		var hour, count int64
		if err := rows.Scan(&provider, &hour, &count); err != nil {
			return nil, err
		}
		if out[provider] == nil {
			out[provider] = map[int64]int64{}
		}
		out[provider][hour] = count
	}
	return out, nil
}

func (s *Store) resetMarkersByProvider(fromTS, toTS int64) (map[string][]int64, error) {
	rows, err := s.db.Query(`
		SELECT provider, reset_at
		FROM quota_exhaustion_events
		WHERE reset_at >= ? AND reset_at < ? AND reset_at > 0 AND provider <> ''
		GROUP BY provider, reset_at
		ORDER BY provider, reset_at`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]int64{}
	for rows.Next() {
		var provider string
		var resetAt int64
		if err := rows.Scan(&provider, &resetAt); err != nil {
			return nil, err
		}
		out[provider] = append(out[provider], resetAt)
	}
	return out, nil
}

func hasResetInHour(resetTimes []int64, hourTS int64) bool {
	for _, resetAt := range resetTimes {
		if floorHour(resetAt) == hourTS {
			return true
		}
	}
	return false
}

func (s *Store) quotaUsedByHour(provider, windowType string, fromTS, toTS int64) (map[int64]float64, error) {
	rows, err := s.db.Query(`
		SELECT (ts / 3600) * 3600 AS hour_ts, MAX(used_percent)
		FROM quota_snapshots
		WHERE provider = ? AND window_type = ? AND ts >= ? AND ts < ?
		GROUP BY hour_ts`, provider, windowType, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var hour int64
		var used float64
		if err := rows.Scan(&hour, &used); err != nil {
			return nil, err
		}
		out[hour] = normalizeUsedFraction(used) * 100
	}
	return out, nil
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

func floorHour(ts int64) int64 {
	return ts - ts%3600
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

func maxProviderPrice(provider string, prices map[TokenDimension]float64) float64 {
	var maxPrice float64
	for dim, price := range prices {
		if dim.Provider == provider && price > maxPrice {
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
