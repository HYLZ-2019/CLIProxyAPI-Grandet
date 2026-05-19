package analytics

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newPricingTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{db: db, dbPath: dbPath, cleanupStop: make(chan struct{})}
}

func TestMigrateDailyTokenPricesAddsAuthIDToPrimaryKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE daily_token_prices (
		    price_date                 TEXT NOT NULL,
		    provider                   TEXT NOT NULL,
		    model                      TEXT NOT NULL,
		    token_type                 TEXT NOT NULL,
		    price_points_per_million   REAL,
		    status                     TEXT NOT NULL DEFAULT '',
		    equation_count             INTEGER DEFAULT 0,
		    residual_rms               REAL DEFAULT 0,
		    residual_mad               REAL DEFAULT 0,
		    source_from_ts             INTEGER DEFAULT 0,
		    source_to_ts               INTEGER DEFAULT 0,
		    solved_at                  INTEGER DEFAULT 0,
		    PRIMARY KEY (price_date, provider, model, token_type)
		);
		INSERT INTO daily_token_prices
		(price_date,provider,model,token_type,price_points_per_million,status,equation_count,
		 residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at)
		VALUES ('2026-05-17','claude','opus','input',123,'solved',1,0,0,1,2,3);
	`)
	if err != nil {
		t.Fatalf("seed old table: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pkCols, err := tablePrimaryKeyColumns(db, "daily_token_prices")
	if err != nil {
		t.Fatalf("primary key columns: %v", err)
	}
	if pkCols["auth_id"] == 0 {
		t.Fatalf("auth_id is not part of primary key: %+v", pkCols)
	}
	var authID string
	var price float64
	if err := db.QueryRow(`SELECT auth_id, price_points_per_million FROM daily_token_prices WHERE provider = 'claude' AND model = 'opus'`).Scan(&authID, &price); err != nil {
		t.Fatalf("query migrated row: %v", err)
	}
	if authID != "" || price != 123 {
		t.Fatalf("unexpected migrated row auth=%q price=%.2f", authID, price)
	}
}

func TestSolveTokenPricesForDateCleanData(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start := date
	features := [][]int64{
		{1_000_000, 0, 0},
		{0, 1_000_000, 0},
		{0, 0, 2_000_000},
		{1_000_000, 1_000_000, 0},
		{2_000_000, 0, 1_000_000},
	}
	prices := []float64{100, 400, 50}
	used := 0.10
	store.InsertQuotaSnapshot(start.Unix(), "claude", "auth-a", "weekly", used, 0)
	for i, f := range features {
		hour := start.Add(time.Duration(i) * time.Hour)
		store.UpsertHourlyAggregate(hour.Unix(), 1, "claude", "auth-a", "opus", f[0], f[1], f[2], f[0]+f[1]+f[2], true)
		target := float64(f[0])/tokenMillion*prices[0] + float64(f[1])/tokenMillion*prices[1] + float64(f[2])/tokenMillion*prices[2]
		used += target / quotaPointsFull
		store.InsertQuotaSnapshot(hour.Add(time.Hour).Unix(), "claude", "auth-a", "weekly", used, 0)
	}

	result, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "solved" || len(result.Providers) != 1 || result.Providers[0].EquationCount != len(features) {
		t.Fatalf("unexpected solve result: %+v", result)
	}
	rows, err := store.TokenPrices("2026-05-17")
	if err != nil {
		t.Fatalf("token prices: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 price rows, got %d", len(rows))
	}
	want := map[string]float64{"input": 100, "output": 400, "cached_input": 50}
	for _, row := range rows {
		if row.AuthID != "auth-a" {
			t.Fatalf("auth_id = %q, want auth-a", row.AuthID)
		}
		if row.Status != "solved" {
			t.Fatalf("%s status = %s", row.TokenType, row.Status)
		}
		if row.PricePointsPerMillion == nil {
			t.Fatalf("%s has nil price", row.TokenType)
		}
		if math.Abs(*row.PricePointsPerMillion-want[row.TokenType]) > 0.01 {
			t.Fatalf("%s price = %.4f, want %.4f", row.TokenType, *row.PricePointsPerMillion, want[row.TokenType])
		}
	}
}

func TestSolveTokenPricesForDateWithResultScopesByAuthID(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start := date
	cases := []struct {
		authID string
		price  float64
	}{
		{authID: "auth-a", price: 100},
		{authID: "auth-b", price: 300},
	}
	for _, tc := range cases {
		used := 10.0
		store.InsertQuotaSnapshot(start.Unix(), "claude", tc.authID, "weekly", used, 0)
		store.InsertQueryLog(start.Add(10*time.Minute).Unix(), 1, "claude", tc.authID, "opus", 1_000_000, 0, 0, 1_000_000, true)
		used += tc.price / quotaPointsFull * 100
		store.InsertQuotaSnapshot(start.Add(20*time.Minute).Unix(), "claude", tc.authID, "weekly", used, 0)
	}

	result, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "solved" || len(result.Providers) != 2 {
		t.Fatalf("unexpected solve result: %+v", result)
	}
	rows, err := store.TokenPrices("2026-05-17")
	if err != nil {
		t.Fatalf("token prices: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 auth-scoped rows, got %d: %+v", len(rows), rows)
	}
	want := map[string]float64{"auth-a": 100, "auth-b": 300}
	for _, row := range rows {
		if row.PricePointsPerMillion == nil {
			t.Fatalf("%s has nil price", row.AuthID)
		}
		if math.Abs(*row.PricePointsPerMillion-want[row.AuthID]) > 0.01 {
			t.Fatalf("%s price = %.4f, want %.4f", row.AuthID, *row.PricePointsPerMillion, want[row.AuthID])
		}
	}
}

func TestSolveTokenPricesForDateWithResultPartialByAuthID(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start := date
	store.InsertQuotaSnapshot(start.Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQueryLog(start.Add(10*time.Minute).Unix(), 1, "claude", "auth-a", "opus", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Add(20*time.Minute).Unix(), "claude", "auth-a", "weekly", 11, 0)
	store.InsertQueryLog(start.Add(10*time.Minute).Unix(), 1, "claude", "auth-b", "opus", 1_000_000, 0, 0, 1_000_000, true)

	result, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "partial" || len(result.Providers) != 2 {
		t.Fatalf("unexpected solve result: %+v", result)
	}
	statuses := map[string]string{}
	for _, provider := range result.Providers {
		statuses[provider.AuthID] = provider.Status
	}
	if statuses["auth-a"] != "solved" || statuses["auth-b"] != "no_quota_snapshots" {
		t.Fatalf("unexpected auth statuses: %+v", statuses)
	}
}

func TestSolveTokenPricesForDateWithResultNoTokenUsage(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)

	result, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "no_token_usage" || len(result.Rows) != 0 || len(result.Providers) != 0 {
		t.Fatalf("unexpected solve result: %+v", result)
	}
}

func TestSolveTokenPricesForDateWithResultInsufficientEquations(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start := date
	store.UpsertHourlyAggregate(start.Unix(), 1, "claude", "auth-a", "opus", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Unix(), "claude", "auth-a", "weekly", 10, 0)

	result, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "insufficient_equations" || len(result.Providers) != 1 {
		t.Fatalf("unexpected solve result: %+v", result)
	}
	if result.Providers[0].EquationCount != 0 || result.Providers[0].RowCount != 1 {
		t.Fatalf("unexpected provider result: %+v", result.Providers[0])
	}
	if len(result.Rows) != 1 || result.Rows[0].Status != "insufficient_equations" {
		t.Fatalf("unexpected rows: %+v", result.Rows)
	}
}

func TestSolveTokenPricesForDateWithResultUsesRecentRawLogs(t *testing.T) {
	store := newPricingTestStore(t)
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	price := 200.0
	used := 10.0
	store.InsertQuotaSnapshot(start.Unix(), "claude", "auth-a", "weekly", used, 0)
	store.InsertQueryLog(start.Add(10*time.Minute).Unix(), 1, "claude", "auth-a", "opus", 1_000_000, 0, 0, 1_000_000, true)
	used += price / quotaPointsFull * 100
	store.InsertQuotaSnapshot(start.Add(20*time.Minute).Unix(), "claude", "auth-a", "weekly", used, 0)

	result, err := store.SolveTokenPricesForDateWithResult(now)
	if err != nil {
		t.Fatalf("solve prices: %v", err)
	}
	if result.Status != "solved" || len(result.Providers) != 1 || result.Providers[0].EquationCount != 1 {
		t.Fatalf("unexpected solve result: %+v", result)
	}
	if result.Providers[0].AuthID != "auth-a" {
		t.Fatalf("provider auth_id = %q, want auth-a", result.Providers[0].AuthID)
	}
	if len(result.Rows) != 1 || result.Rows[0].AuthID != "auth-a" || result.Rows[0].PricePointsPerMillion == nil {
		t.Fatalf("unexpected rows: %+v", result.Rows)
	}
	if math.Abs(*result.Rows[0].PricePointsPerMillion-price) > 0.01 {
		t.Fatalf("price = %.4f, want %.4f", *result.Rows[0].PricePointsPerMillion, price)
	}
}

func TestBuildQuotaEquationsSkipsResetsAndSupportsPercentScales(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		hour := start.Add(time.Duration(i) * time.Hour)
		store.UpsertHourlyAggregate(hour.Unix(), 1, "codex", "auth-a", "gpt", 1_000_000, 0, 0, 1_000_000, true)
	}
	store.InsertQuotaSnapshot(start.Unix(), "codex", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(time.Hour).Unix(), "codex", "auth-a", "weekly", 20, 0)
	store.InsertQuotaSnapshot(start.Add(2*time.Hour).Unix(), "codex", "auth-a", "weekly", 15, 0)
	store.InsertQuotaSnapshot(start.Add(3*time.Hour).Unix(), "codex", "auth-a", "weekly", 0.30, 0)

	equations, err := store.buildQuotaEquationsForAuth("codex", "auth-a", "weekly", []TokenDimension{{Provider: "codex", AuthID: "auth-a", Model: "gpt", TokenType: "input"}}, start.Unix(), start.Add(4*time.Hour).Unix())
	if err != nil {
		t.Fatalf("build equations: %v", err)
	}
	if len(equations) != 2 {
		t.Fatalf("expected 2 equations after reset filtering, got %d", len(equations))
	}
	if math.Abs(equations[0].target-1500) > 0.01 || math.Abs(equations[1].target-1000) > 0.01 {
		t.Fatalf("unexpected targets: %.2f %.2f", equations[0].target, equations[1].target)
	}
}

func TestProviderQuotaLinesResetRules(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	price := 100.0
	if err := store.UpsertTokenPrice(TokenPriceRow{PriceDate: time.Now().Format("2006-01-02"), Provider: "gemini", AuthID: "auth-a", Model: "pro", TokenType: "input", PricePointsPerMillion: &price, Status: "solved"}); err != nil {
		t.Fatalf("seed price: %v", err)
	}
	for i := 0; i < 4; i++ {
		hour := start.Add(time.Duration(i) * time.Hour)
		store.UpsertHourlyAggregate(hour.Unix(), 1, "gemini", "auth-a", "pro", 1_000_000, 0, 0, 1_000_000, true)
	}
	used := []float64{10, 20, 5, 15}
	for i, v := range used {
		store.InsertQuotaSnapshot(start.Add(time.Duration(i)*time.Hour).Unix(), "gemini", "auth-a", "weekly", v, 0)
	}
	store.InsertQuotaEvent(start.Add(time.Hour).Unix(), "gemini", "auth-a", "pro")
	store.InsertQuotaEvent(start.Add(90*time.Minute).Unix(), "gemini", "auth-a", "pro", start.Add(210*time.Minute).Unix())
	end := start.Add(25 * time.Hour)

	resp, err := store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 25 {
		t.Fatalf("unexpected series shape: %+v", resp)
	}
	points := resp.Series[0].Points
	if points[0].CLIProxyCumulativePoints != 100 || points[1].CLIProxyCumulativePoints != 200 || points[2].CLIProxyCumulativePoints != 100 {
		t.Fatalf("unexpected default cumulative points: %+v", points)
	}
	if points[1].QuotaEventsCount != 2 {
		t.Fatalf("expected 429 events on second point")
	}
	if len(resp.Series[0].ResetMarkers) != 1 || resp.Series[0].ResetMarkers[0].ResetAt != start.Add(210*time.Minute).Unix() {
		t.Fatalf("unexpected reset markers: %+v", resp.Series[0].ResetMarkers)
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), end.Unix(), true, false, "")
	if err != nil {
		t.Fatalf("provider lines with 429 reset: %v", err)
	}
	points = resp.Series[0].Points
	if points[0].CLIProxyCumulativePoints != 100 || points[1].CLIProxyCumulativePoints != 100 || points[2].CLIProxyCumulativePoints != 100 {
		t.Fatalf("unexpected reset-on-429 cumulative points: %+v", points)
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), end.Unix(), false, true, "")
	if err != nil {
		t.Fatalf("provider lines with refresh reset: %v", err)
	}
	points = resp.Series[0].Points
	if points[0].CLIProxyCumulativePoints != 100 || points[1].CLIProxyCumulativePoints != 200 || points[2].CLIProxyCumulativePoints != 100 || points[3].CLIProxyCumulativePoints != 100 {
		t.Fatalf("unexpected reset-on-refresh cumulative points: %+v", points)
	}
}

func TestProviderQuotaLinesAreScopedByAuthID(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	priceA := 100.0
	priceB := 300.0
	if err := store.UpsertTokenPrice(TokenPriceRow{PriceDate: time.Now().Format("2006-01-02"), Provider: "claude", AuthID: "auth-a", Model: "opus", TokenType: "input", PricePointsPerMillion: &priceA, Status: "solved"}); err != nil {
		t.Fatalf("seed claude auth-a price: %v", err)
	}
	if err := store.UpsertTokenPrice(TokenPriceRow{PriceDate: time.Now().Format("2006-01-02"), Provider: "claude", AuthID: "auth-b", Model: "opus", TokenType: "input", PricePointsPerMillion: &priceB, Status: "solved"}); err != nil {
		t.Fatalf("seed claude auth-b price: %v", err)
	}
	if err := store.UpsertTokenPrice(TokenPriceRow{PriceDate: time.Now().Format("2006-01-02"), Provider: "gemini", AuthID: "auth-a", Model: "pro", TokenType: "input", PricePointsPerMillion: &priceA, Status: "solved"}); err != nil {
		t.Fatalf("seed gemini price: %v", err)
	}

	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "opus", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-b", "opus", 2_000_000, 0, 0, 2_000_000, true)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "gemini", "auth-a", "pro", 3_000_000, 0, 0, 3_000_000, true)

	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-b", "weekly", 20, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "gemini", "auth-a", "weekly", 30, 0)
	store.InsertQuotaEvent(start.Add(time.Minute).Unix(), "claude", "auth-a", "opus", start.Add(6*time.Minute).Unix())

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(10*time.Minute).Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 3 {
		t.Fatalf("expected 3 auth series, got %d: %+v", len(resp.Series), resp.Series)
	}
	series := map[quotaSeriesKey]ProviderQuotaSeries{}
	for _, row := range resp.Series {
		series[quotaSeriesKey{provider: row.Provider, authID: row.AuthID}] = row
	}
	claudeA := series[quotaSeriesKey{provider: "claude", authID: "auth-a"}]
	claudeB := series[quotaSeriesKey{provider: "claude", authID: "auth-b"}]
	geminiA := series[quotaSeriesKey{provider: "gemini", authID: "auth-a"}]
	if claudeA.Points[0].QuotaUsedPercent != 10 || claudeA.Points[0].CLIProxyCumulativePoints != 100 || claudeA.Points[0].QuotaEventsCount != 1 || len(claudeA.ResetMarkers) != 1 {
		t.Fatalf("unexpected claude auth-a series: %+v", claudeA)
	}
	if claudeB.Points[0].QuotaUsedPercent != 20 || claudeB.Points[0].CLIProxyCumulativePoints != 600 || claudeB.Points[0].QuotaEventsCount != 0 || len(claudeB.ResetMarkers) != 0 {
		t.Fatalf("unexpected claude auth-b series: %+v", claudeB)
	}
	if geminiA.Points[0].QuotaUsedPercent != 30 || geminiA.Points[0].CLIProxyCumulativePoints != 300 {
		t.Fatalf("unexpected gemini auth-a series: %+v", geminiA)
	}
}

func TestHourlyRowsUsesFiveMinuteBucketsForShortRanges(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "opus", 100, 200, 50, 350, true)
	store.InsertQueryLog(start.Add(6*time.Minute).Unix(), 1, "claude", "auth-a", "opus", 300, 400, 0, 700, false)

	rows, err := store.HourlyRows(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("hourly rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].BucketSeconds != analyticsFiveMinuteBucketSeconds || rows[0].HourTS != start.Add(5*time.Minute).Unix() {
		t.Fatalf("unexpected newest bucket: %+v", rows[0])
	}
	if rows[0].RequestCount != 1 || rows[0].ErrorCount != 1 || rows[0].TotalTokensSum != 700 {
		t.Fatalf("unexpected newest aggregate: %+v", rows[0])
	}
	if rows[1].BucketSeconds != analyticsFiveMinuteBucketSeconds || rows[1].HourTS != start.Unix() {
		t.Fatalf("unexpected oldest bucket: %+v", rows[1])
	}
	if rows[1].RequestCount != 1 || rows[1].SuccessCount != 1 || rows[1].TotalTokensSum != 350 {
		t.Fatalf("unexpected oldest aggregate: %+v", rows[1])
	}
}

func TestHourlyRowsUsesHourlyAggregatesForLongRanges(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.UpsertHourlyAggregate(start.Unix(), 1, "claude", "auth-a", "opus", 100, 200, 50, 350, true)

	rows, err := store.HourlyRows(start.Unix(), start.Add(25*time.Hour).Unix())
	if err != nil {
		t.Fatalf("hourly rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].BucketSeconds != analyticsHourBucketSeconds || rows[0].BucketTS != start.Unix() {
		t.Fatalf("unexpected hourly bucket metadata: %+v", rows[0])
	}
}

func TestProviderQuotaLinesUsesLatestSnapshotInFiveMinuteBucket(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 80, 0)
	store.InsertQuotaSnapshot(start.Add(20*time.Second).Unix(), "claude", "auth-a", "weekly", 10, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(10*time.Minute).Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 2 {
		t.Fatalf("unexpected series: %+v", resp)
	}
	first := resp.Series[0].Points[0]
	if first.BucketSeconds != analyticsFiveMinuteBucketSeconds || first.QuotaUsedPercent != 10 {
		t.Fatalf("expected latest snapshot in bucket, got %+v", first)
	}
}

func TestClassifyQuotaWindow(t *testing.T) {
	cases := map[string]string{
		"":                                "",
		"five_hour":                       "5h",
		"FIVE_HOUR":                       "5h",
		"primary_five_hour":               "5h",
		"code_five_hour":                  "5h",
		"code_review_primary_five_hour":   "5h",
		"additional_foo_five_hour":        "5h",
		"five-hour":                       "5h",
		"fivehour":                        "5h",
		"seven_day":                       "7d",
		"seven_day_oauth_apps":            "7d",
		"seven_day_opus":                  "7d",
		"weekly":                          "7d",
		"code_weekly":                     "7d",
		"primary_weekly":                  "7d",
		"gemini-2.5-pro:REQUESTS":         "",
		"daily-default":                   "",
		"iguana_necktie":                  "",
	}
	for input, want := range cases {
		if got := classifyQuotaWindow(input); got != want {
			t.Errorf("classifyQuotaWindow(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProviderQuotaLinesWindowClassFiltering(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	// Claude has both five_hour and seven_day; the chooser should follow the requested class.
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "five_hour", 50, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "seven_day", 12, 0)
	// Gemini has only model-specific windows that don't classify; it should fall back to whatever exists.
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "gemini-cli", "auth-g", "gemini-2.5-pro:REQUESTS", 30, 0)

	end := start.Add(10 * time.Minute)

	resp, err := store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines 5h: %v", err)
	}
	got := map[string]string{}
	for _, s := range resp.Series {
		got[s.Provider+"|"+s.AuthID] = s.WindowType
	}
	if got["claude|auth-a"] != "five_hour" {
		t.Fatalf("claude with 5h class should pick five_hour, got %q", got["claude|auth-a"])
	}
	if got["gemini-cli|auth-g"] != "gemini-2.5-pro:REQUESTS" {
		t.Fatalf("gemini fallback under 5h class should keep its model window, got %q", got["gemini-cli|auth-g"])
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "7d")
	if err != nil {
		t.Fatalf("provider quota lines 7d: %v", err)
	}
	got = map[string]string{}
	for _, s := range resp.Series {
		got[s.Provider+"|"+s.AuthID] = s.WindowType
	}
	if got["claude|auth-a"] != "seven_day" {
		t.Fatalf("claude with 7d class should pick seven_day, got %q", got["claude|auth-a"])
	}
	if got["gemini-cli|auth-g"] != "gemini-2.5-pro:REQUESTS" {
		t.Fatalf("gemini fallback under 7d class should keep its model window, got %q", got["gemini-cli|auth-g"])
	}
}

func TestBuildQuotaEquationsUsesSnapshotIntervalsFromQueryLogs(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "opus", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(start.Add(20*time.Minute).Unix(), 1, "claude", "auth-a", "opus", 2_000_000, 0, 0, 2_000_000, true)
	store.InsertQuotaSnapshot(start.Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Minute).Unix(), "claude", "auth-a", "weekly", 20, 0)
	store.InsertQuotaSnapshot(start.Add(30*time.Minute).Unix(), "claude", "auth-a", "weekly", 30, 0)

	equations, err := store.buildQuotaEquationsForAuth("claude", "auth-a", "weekly", []TokenDimension{{Provider: "claude", AuthID: "auth-a", Model: "opus", TokenType: "input"}}, start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("build equations: %v", err)
	}
	if len(equations) != 2 {
		t.Fatalf("expected 2 equations, got %d", len(equations))
	}
	if equations[0].fromTS != start.Add(10*time.Minute).Unix() || math.Abs(equations[0].features[0]-2) > 0.01 {
		t.Fatalf("unexpected newest interval equation: %+v", equations[0])
	}
	if equations[1].fromTS != start.Unix() || math.Abs(equations[1].features[0]-1) > 0.01 {
		t.Fatalf("unexpected oldest interval equation: %+v", equations[1])
	}
}

func TestBuildQuotaEquationsFallsBackToProportionalHourlyFeatures(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.UpsertHourlyAggregate(start.Unix(), 1, "claude", "auth-a", "opus", 2_000_000, 0, 0, 2_000_000, true)
	store.InsertQuotaSnapshot(start.Add(15*time.Minute).Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(45*time.Minute).Unix(), "claude", "auth-a", "weekly", 20, 0)

	equations, err := store.buildQuotaEquationsForAuth("claude", "auth-a", "weekly", []TokenDimension{{Provider: "claude", AuthID: "auth-a", Model: "opus", TokenType: "input"}}, start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("build equations: %v", err)
	}
	if len(equations) != 1 {
		t.Fatalf("expected 1 equation, got %d", len(equations))
	}
	if math.Abs(equations[0].features[0]-1) > 0.01 {
		t.Fatalf("expected half-hour proportional feature, got %+v", equations[0])
	}
}
