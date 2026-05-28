package analytics

import (
	"context"
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

func TestOfficialTokenPriceUSDMatchesKnownModelsAndUnknown(t *testing.T) {
	cases := []struct {
		provider  string
		model     string
		tokenType string
		want      float64
	}{
		{provider: "claude", model: "claude-opus-4-7", tokenType: "input", want: 5},
		{provider: "claude", model: "claude-opus-4-6", tokenType: "cached_input", want: 0.5},
		{provider: "claude", model: "claude-opus-4-5", tokenType: "output", want: 25},
		{provider: "claude", model: "claude-opus-4-1", tokenType: "input", want: 15},
		{provider: "claude", model: "claude-opus-4", tokenType: "output", want: 75},
		{provider: "claude", model: "claude-sonnet-4-6", tokenType: "input", want: 3},
		{provider: "claude", model: "claude-sonnet-4-5", tokenType: "output", want: 15},
		{provider: "claude", model: "claude-sonnet-4", tokenType: "cached_input", want: 0.3},
		{provider: "claude", model: "claude-haiku-4-5", tokenType: "output", want: 5},
		{provider: "claude", model: "claude-haiku-3-5", tokenType: "input", want: 0.8},
		{provider: "claude", model: "claude-haiku-3-5", tokenType: "cached_input", want: 0.08},
		{provider: "claude", model: "claude-haiku-3-5", tokenType: "output", want: 4},
		{provider: "codex", model: "gpt-5.5", tokenType: "input", want: 5},
		{provider: "codex", model: "gpt-5.5", tokenType: "cached_input", want: 0.5},
		{provider: "codex", model: "gpt-5.5", tokenType: "output", want: 30},
		{provider: "openai", model: "gpt-5.5-pro", tokenType: "input", want: 30},
		{provider: "openai", model: "gpt-5.5-pro", tokenType: "output", want: 180},
		{provider: "codex", model: "gpt-5.4", tokenType: "input", want: 2.5},
		{provider: "codex", model: "gpt-5.4", tokenType: "cached_input", want: 0.25},
		{provider: "codex", model: "gpt-5.4", tokenType: "output", want: 15},
		{provider: "openai", model: "gpt-5.4-mini", tokenType: "input", want: 0.75},
		{provider: "openai", model: "gpt-5.4-nano", tokenType: "output", want: 1.25},
		{provider: "codex", model: "gpt-5-codex", tokenType: "input", want: 1.25},
		{provider: "openai", model: "gpt-5-mini", tokenType: "cached_input", want: 0.025},
		{provider: "openai", model: "gpt-4.1-nano", tokenType: "output", want: 0.4},
		{provider: "gemini-cli", model: "gemini-2.5-flash-lite", tokenType: "cached_input", want: 0.01},
		{provider: "gemini", model: "gemini-3.1-pro-preview", tokenType: "output", want: 12},
	}
	for _, tc := range cases {
		got, ok := officialTokenPriceUSD(tc.provider, tc.model, tc.tokenType)
		if !ok || math.Abs(got-tc.want) > 0.0001 {
			t.Fatalf("officialTokenPriceUSD(%q,%q,%q) = %.4f,%v want %.4f,true", tc.provider, tc.model, tc.tokenType, got, ok, tc.want)
		}
	}
	if got, ok := officialTokenPriceUSD("openai", "gpt-5.5-pro", "cached_input"); ok || got != 0 {
		t.Fatalf("gpt-5.5-pro cached input price = %.4f,%v", got, ok)
	}
	if got, ok := officialTokenPriceUSD("claude", "unknown-model", "input"); ok || got != 0 {
		t.Fatalf("unknown model price = %.4f,%v", got, ok)
	}
	if got, ok := officialTokenPriceUSD("gemini", "gemini-3.1-pro-preview", "cached_input"); ok || got != 0 {
		t.Fatalf("gemini 3 cached input price = %.4f,%v", got, ok)
	}
}

func TestOfficialTokenPricesForUsageReturnsOfficialAndUnknownRows(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-sonnet-4-6", 1_000, 2_000, 300, 3_300, true)
	store.InsertQueryLog(start.Add(2*time.Minute).Unix(), 1, "claude", "auth-a", "unknown-model", 500, 0, 0, 500, true)

	rows, err := store.OfficialTokenPricesForUsage(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("official prices: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 token price rows, got %d: %+v", len(rows), rows)
	}
	prices := map[string]TokenPriceRow{}
	for _, row := range rows {
		prices[row.Model+"|"+row.TokenType] = row
	}
	if row := prices["claude-sonnet-4-6|input"]; row.Status != "official" || row.PriceUSDPerMillion == nil || *row.PriceUSDPerMillion != 3 {
		t.Fatalf("unexpected input price row: %+v", row)
	}
	if row := prices["claude-sonnet-4-6|output"]; row.Status != "official" || row.PriceUSDPerMillion == nil || *row.PriceUSDPerMillion != 15 {
		t.Fatalf("unexpected output price row: %+v", row)
	}
	if row := prices["claude-sonnet-4-6|cached_input"]; row.Status != "official" || row.PriceUSDPerMillion == nil || *row.PriceUSDPerMillion != 0.3 {
		t.Fatalf("unexpected cached price row: %+v", row)
	}
	if row := prices["unknown-model|input"]; row.Status != "unknown" || row.PriceUSDPerMillion != nil {
		t.Fatalf("unexpected unknown price row: %+v", row)
	}
}

func TestProviderQuotaLinesResetRulesUseUSD(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		hour := start.Add(time.Duration(i) * time.Hour)
		store.UpsertHourlyAggregate(hour.Unix(), 1, "gemini", "auth-a", "gemini-2.5-pro", 1_000_000, 0, 0, 1_000_000, true)
	}
	used := []float64{10, 20, 5, 15}
	for i, v := range used {
		store.InsertQuotaSnapshot(start.Add(time.Duration(i)*time.Hour).Unix(), "gemini", "auth-a", "weekly", v, 0)
	}
	store.InsertQuotaEvent(start.Add(time.Hour).Unix(), "gemini", "auth-a", "gemini-2.5-pro")
	store.InsertQuotaEvent(start.Add(90*time.Minute).Unix(), "gemini", "auth-a", "gemini-2.5-pro", start.Add(210*time.Minute).Unix())
	end := start.Add(25 * time.Hour)

	resp, err := store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 25 {
		t.Fatalf("unexpected series shape: %+v", resp)
	}
	points := resp.Series[0].Points
	if !near(points[0].CLIProxyCumulativeUSD, 1.25) || !near(points[1].CLIProxyCumulativeUSD, 2.5) || !near(points[2].CLIProxyCumulativeUSD, 3.75) {
		t.Fatalf("unexpected default cumulative USD: %+v", points[:4])
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
	if !near(points[0].CLIProxyCumulativeUSD, 1.25) || !near(points[1].CLIProxyCumulativeUSD, 1.25) || !near(points[2].CLIProxyCumulativeUSD, 2.5) {
		t.Fatalf("unexpected reset-on-429 cumulative USD: %+v", points[:4])
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), end.Unix(), false, true, "")
	if err != nil {
		t.Fatalf("provider lines with refresh reset: %v", err)
	}
	points = resp.Series[0].Points
	if !near(points[0].CLIProxyCumulativeUSD, 1.25) || !near(points[1].CLIProxyCumulativeUSD, 2.5) || !near(points[2].CLIProxyCumulativeUSD, 3.75) || !near(points[3].CLIProxyCumulativeUSD, 1.25) {
		t.Fatalf("unexpected reset-on-refresh cumulative USD: %+v", points[:4])
	}
}

func TestProviderQuotaLinesQuotaDropResetThresholds(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	used := []float64{10, 6, 2.9, 12, 6.9}
	for i, v := range used {
		ts := start.Add(time.Duration(i) * 5 * time.Minute)
		store.InsertQueryLog(ts.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
		store.InsertQuotaSnapshot(ts.Unix(), "claude", "auth-a", "five_hour", v, 0)
	}

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(time.Duration(len(used))*5*time.Minute).Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != len(used) {
		t.Fatalf("unexpected series: %+v", resp.Series)
	}
	points := resp.Series[0].Points
	if !near(points[1].CLIProxyCumulativeUSD, 10) {
		t.Fatalf("small quota drop should not reset cumulative: %+v", points)
	}
	if !near(points[2].CLIProxyCumulativeUSD, 5) {
		t.Fatalf("drop below 3%% should reset cumulative: %+v", points)
	}
	if !near(points[4].CLIProxyCumulativeUSD, 5) {
		t.Fatalf("drop over 5%% within 5 minutes should reset cumulative: %+v", points)
	}
}

func TestProviderQuotaLinesIncludesDedupedSnapshotResetMarkers(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	resetAt := start.Add(30 * time.Minute).Unix()
	store.InsertQuotaSnapshot(start.Unix(), "codex", "auth-plus", "code_five_hour", 20, resetAt)
	store.InsertQuotaSnapshot(start.Add(5*time.Minute).Unix(), "codex", "auth-plus", "code_five_hour", 21, resetAt+1)
	store.InsertQuotaSnapshot(start.Unix(), "codex", "auth-plus", "code_weekly", 12, start.Add(7*24*time.Hour).Unix())

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(time.Hour).Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected one series, got %+v", resp.Series)
	}
	markers := resp.Series[0].ResetMarkers
	if len(markers) != 1 || markers[0].ResetAt != resetAt {
		t.Fatalf("expected deduped 5h snapshot reset marker, got %+v", markers)
	}
	if markers[0].Points <= 0 {
		t.Fatalf("expected reset marker to be placed on quota axis, got %+v", markers[0])
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), start.Add(time.Hour).Unix(), false, false, "7d")
	if err != nil {
		t.Fatalf("provider quota lines 7d: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].ResetMarkers) != 0 {
		t.Fatalf("5h reset markers should not appear on 7d chart: %+v", resp.Series)
	}
}

func TestProviderQuotaLinesSnapshotResetMarkersRequireUsageAtLeastTwoPercent(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	ignoredResetAt := start.Add(30 * time.Minute).Unix()
	keptResetAt := start.Add(35 * time.Minute).Unix()
	store.InsertQuotaSnapshot(start.Unix(), "codex", "auth-plus", "code_five_hour", 1, ignoredResetAt)
	store.InsertQuotaSnapshot(start.Add(5*time.Minute).Unix(), "codex", "auth-plus", "code_five_hour", 2, keptResetAt)
	store.InsertQuotaSnapshot(start.Add(10*time.Minute).Unix(), "codex", "auth-plus", "code_five_hour", 3, keptResetAt+1)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(time.Hour).Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected one series, got %+v", resp.Series)
	}
	markers := resp.Series[0].ResetMarkers
	if len(markers) != 1 || markers[0].ResetAt != keptResetAt {
		t.Fatalf("expected only >=2%% reset_at with minute dedupe, got %+v", markers)
	}
}

func TestProviderQuotaLinesCumulativeIncludesUsageSincePriorReset(t *testing.T) {
	store := newPricingTestStore(t)
	reset := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	from := reset.Add(2 * time.Hour)
	to := from.Add(15 * time.Minute)

	store.InsertQueryLog(reset.Add(-30*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(reset.Add(5*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(reset.Add(65*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(from.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(reset.Unix(), "claude", "auth-a", "five_hour", 2, reset.Unix())
	store.InsertQuotaSnapshot(from.Unix(), "claude", "auth-a", "five_hour", 5, from.Add(3*time.Hour).Unix())

	resp, err := store.ProviderQuotaLines(from.Unix(), to.Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected one series, got %+v", resp.Series)
	}
	points := resp.Series[0].Points
	if len(points) != 3 || points[0].BucketTS != from.Unix() {
		t.Fatalf("display range should still start at from with 3 points, got %+v", points)
	}
	if !near(points[0].CLIProxyCumulativeUSD, 15) {
		t.Fatalf("first visible cumulative USD = %.4f, want 15", points[0].CLIProxyCumulativeUSD)
	}
	if !near(points[0].CLIProxyHourUSD, 5) {
		t.Fatalf("first visible bucket USD = %.4f, want 5", points[0].CLIProxyHourUSD)
	}
	if len(resp.Series[0].ResetMarkers) != 0 {
		t.Fatalf("hidden reset marker should seed cumulative but not be displayed: %+v", resp.Series[0].ResetMarkers)
	}
}

func TestProviderQuotaLinesCumulativeUsesWindowSpecificReset(t *testing.T) {
	store := newPricingTestStore(t)
	weeklyReset := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	fiveHourReset := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	from := fiveHourReset.Add(2 * time.Hour)
	to := from.Add(10 * time.Minute)

	store.InsertQueryLog(weeklyReset.Add(time.Hour).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(fiveHourReset.Add(time.Hour).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(from.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(weeklyReset.Unix(), "claude", "auth-a", "seven_day", 2, weeklyReset.Unix())
	store.InsertQuotaSnapshot(fiveHourReset.Unix(), "claude", "auth-a", "five_hour", 2, fiveHourReset.Unix())
	store.InsertQuotaSnapshot(from.Unix(), "claude", "auth-a", "seven_day", 10, weeklyReset.Add(7*24*time.Hour).Unix())
	store.InsertQuotaSnapshot(from.Unix(), "claude", "auth-a", "five_hour", 10, from.Add(3*time.Hour).Unix())

	resp, err := store.ProviderQuotaLines(from.Unix(), to.Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines 5h: %v", err)
	}
	if len(resp.Series) != 1 || !near(resp.Series[0].Points[0].CLIProxyCumulativeUSD, 10) {
		t.Fatalf("5h cumulative should start at 5h reset, got %+v", resp.Series)
	}

	resp, err = store.ProviderQuotaLines(from.Unix(), to.Unix(), false, false, "7d")
	if err != nil {
		t.Fatalf("provider quota lines 7d: %v", err)
	}
	if len(resp.Series) != 1 || !near(resp.Series[0].Points[0].CLIProxyCumulativeUSD, 15) {
		t.Fatalf("7d cumulative should start at weekly reset, got %+v", resp.Series)
	}
}

func TestProviderQuotaLinesAreScopedByAuthID(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-b", "claude-opus-4-7", 2_000_000, 0, 0, 2_000_000, true)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "gemini", "auth-a", "gemini-2.5-pro", 3_000_000, 0, 0, 3_000_000, true)

	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-b", "weekly", 20, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "gemini", "auth-a", "weekly", 30, 0)
	store.InsertQuotaEvent(start.Add(time.Minute).Unix(), "claude", "auth-a", "claude-opus-4-7", start.Add(6*time.Minute).Unix())

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
	if claudeA.Points[0].QuotaUsedPercent != 10 || !near(claudeA.Points[0].CLIProxyCumulativeUSD, 5) || claudeA.Points[0].QuotaEventsCount != 1 || len(claudeA.ResetMarkers) != 1 {
		t.Fatalf("unexpected claude auth-a series: %+v", claudeA)
	}
	if claudeB.Points[0].QuotaUsedPercent != 20 || !near(claudeB.Points[0].CLIProxyCumulativeUSD, 10) || claudeB.Points[0].QuotaEventsCount != 0 || len(claudeB.ResetMarkers) != 0 {
		t.Fatalf("unexpected claude auth-b series: %+v", claudeB)
	}
	if geminiA.Points[0].QuotaUsedPercent != 30 || !near(geminiA.Points[0].CLIProxyCumulativeUSD, 3.75) {
		t.Fatalf("unexpected gemini auth-a series: %+v", geminiA)
	}
}

func TestProviderQuotaLinesEstimateQuotaUSD(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(start.Add(6*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQuotaSnapshot(start.Add(5*time.Minute+10*time.Second).Unix(), "claude", "auth-a", "weekly", 20, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(10*time.Minute).Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected 1 series, got %d: %+v", len(resp.Series), resp.Series)
	}
	if !near(resp.Series[0].EstimatedQuotaUSD, 50) {
		t.Fatalf("estimated quota USD = %.4f, want 50", resp.Series[0].EstimatedQuotaUSD)
	}
	if !near(resp.Series[0].InputUSDPerMillion, 5) {
		t.Fatalf("input USD per million = %.4f, want 5", resp.Series[0].InputUSDPerMillion)
	}
	if resp.Series[0].InputPriceModel != "claude-opus-4-7" {
		t.Fatalf("input price model = %q, want claude-opus-4-7", resp.Series[0].InputPriceModel)
	}
}

func TestProviderQuotaLinesReturnsUsageModelWhenInputPriceUnknown(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-future-unknown", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "five_hour", 10, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(10*time.Minute).Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected 1 series, got %d: %+v", len(resp.Series), resp.Series)
	}
	if resp.Series[0].InputPriceModel != "claude-future-unknown" {
		t.Fatalf("input price model = %q, want claude-future-unknown", resp.Series[0].InputPriceModel)
	}
	if resp.Series[0].InputUSDPerMillion != 0 {
		t.Fatalf("input USD per million = %.4f, want 0", resp.Series[0].InputUSDPerMillion)
	}
}

func TestProviderQuotaLinesEstimateQuotaUSDAveragesFiveMinuteBucketIncreases(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 10, 0)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Add(5*time.Minute+10*time.Second).Unix(), "claude", "auth-a", "weekly", 20, 0)
	store.InsertQueryLog(start.Add(6*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 2_000_000, 0, 0, 2_000_000, true)
	store.InsertQuotaSnapshot(start.Add(10*time.Minute+10*time.Second).Unix(), "claude", "auth-a", "weekly", 40, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(15*time.Minute).Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected 1 series, got %d: %+v", len(resp.Series), resp.Series)
	}
	if !near(resp.Series[0].EstimatedQuotaUSD, 50) {
		t.Fatalf("estimated quota USD = %.4f, want 50", resp.Series[0].EstimatedQuotaUSD)
	}
}

func TestQuotaEstimateWindowUsesWindowClass(t *testing.T) {
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	from, bucketSeconds, minDelta := quotaEstimateWindow("5h", "five_hour", start.Add(-7*24*time.Hour).Unix(), start.Unix())
	if from != start.Add(-24*time.Hour).Unix() || bucketSeconds != analyticsFiveMinuteBucketSeconds || minDelta != 1 {
		t.Fatalf("unexpected 5h estimate window from=%d bucket=%d minDelta=%.1f", from, bucketSeconds, minDelta)
	}
	from, bucketSeconds, minDelta = quotaEstimateWindow("7d", "weekly", start.Add(-7*24*time.Hour).Unix(), start.Unix())
	if from != start.Add(-8*24*time.Hour).Unix() || bucketSeconds != analyticsFiveMinuteBucketSeconds || minDelta != 3 {
		t.Fatalf("unexpected 7d estimate window from=%d bucket=%d minDelta=%.1f", from, bucketSeconds, minDelta)
	}
}

func TestProviderQuotaLinesEstimateQuotaUSDRequiresPositiveDeltas(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQueryLog(start.Add(6*time.Minute).Unix(), 1, "claude", "auth-a", "claude-opus-4-7", 1_000_000, 0, 0, 1_000_000, true)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "weekly", 20, 0)
	store.InsertQuotaSnapshot(start.Add(5*time.Minute+10*time.Second).Unix(), "claude", "auth-a", "weekly", 20, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(10*time.Minute).Unix(), false, false, "")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected 1 series, got %d: %+v", len(resp.Series), resp.Series)
	}
	if resp.Series[0].EstimatedQuotaUSD != 0 {
		t.Fatalf("estimated quota USD = %.4f, want 0", resp.Series[0].EstimatedQuotaUSD)
	}
}

func TestEstimateQuotaUSDAccumulatesIntegerStepWindows(t *testing.T) {
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC).Unix()
	usedByBucket := map[int64]float64{
		start:         10,
		start + 5*60:  10,
		start + 10*60: 11,
		start + 15*60: 11,
		start + 20*60: 13,
	}
	usdByBucket := map[int64]float64{
		start:         1,
		start + 5*60:  2,
		start + 10*60: 3,
		start + 15*60: 4,
	}

	got := estimateQuotaUSDFromBuckets(start, start+20*60, analyticsFiveMinuteBucketSeconds, 3, usedByBucket, usdByBucket)
	if !near(got, 10.0/3.0*100) {
		t.Fatalf("estimated quota USD = %.4f, want %.4f", got, 10.0/3.0*100)
	}
}

func TestEstimateQuotaUSDRequiresMinimumAccumulatedDelta(t *testing.T) {
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC).Unix()
	usedByBucket := map[int64]float64{
		start:         10,
		start + 5*60:  11,
		start + 10*60: 12,
	}
	usdByBucket := map[int64]float64{
		start:        1,
		start + 5*60: 1,
	}

	got := estimateQuotaUSDFromBuckets(start, start+10*60, analyticsFiveMinuteBucketSeconds, 3, usedByBucket, usdByBucket)
	if got != 0 {
		t.Fatalf("estimated quota USD = %.4f, want 0", got)
	}
}

func TestSummaryAndAggregateRowsDeriveTotalTokensFromSplitCounts(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	if _, err := store.db.Exec(`
		INSERT INTO query_logs
		(ts,client_key_id,provider,auth_id,model,input_tokens,output_tokens,cached_tokens,total_tokens,success)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, start.Unix(), 1, "claude", "auth-a", "opus", 100, 200, 50, 1, 1); err != nil {
		t.Fatalf("insert query log: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO hourly_aggregates
		(hour_ts,client_key_id,provider,auth_id,model,request_count,success_count,error_count,input_tokens_sum,output_tokens_sum,cached_tokens_sum,total_tokens_sum)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, start.Unix(), 1, "claude", "auth-a", "opus", 1, 1, 0, 100, 200, 50, 1); err != nil {
		t.Fatalf("insert hourly aggregate: %v", err)
	}

	summary, err := store.Summary(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.InputTokens != 100 || summary.OutputTokens != 200 || summary.CachedTokens != 50 || summary.TotalTokens != 350 {
		t.Fatalf("unexpected summary totals: %+v", summary)
	}

	shortRows, err := store.HourlyRows(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("short hourly rows: %v", err)
	}
	if len(shortRows) != 1 || shortRows[0].TotalTokensSum != 350 {
		t.Fatalf("unexpected short range totals: %+v", shortRows)
	}

	longRows, err := store.HourlyRows(start.Unix(), start.Add(25*time.Hour).Unix())
	if err != nil {
		t.Fatalf("long hourly rows: %v", err)
	}
	if len(longRows) != 1 || longRows[0].TotalTokensSum != 350 {
		t.Fatalf("unexpected long range totals: %+v", longRows)
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

func TestByClientUsesQueryLogsForShortRanges(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "opus", 100, 200, 50, 350, true)
	store.InsertQueryLog(start.Add(2*time.Minute).Unix(), 2, "claude", "auth-a", "opus", 300, 400, 0, 700, false)

	rows, err := store.ByClient(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("by client: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 client rows, got %d: %+v", len(rows), rows)
	}
	seen := map[int]int64{}
	for _, row := range rows {
		seen[intMapValue(row, "client_key_id")] = int64MapValue(t, row, "request_count")
	}
	if seen[1] != 1 || seen[2] != 1 {
		t.Fatalf("unexpected client rows from query_logs: %+v", rows)
	}
}

func TestByModelUsesQueryLogsAndReturnsTokenBreakdownForShortRanges(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQueryLog(start.Add(time.Minute).Unix(), 1, "claude", "auth-a", "opus", 100, 200, 50, 350, true, 30, 40, 60)

	rows, err := store.ByModel(start.Unix(), start.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("by model: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model row, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if int64MapValue(t, row, "input_tokens_sum") != 100 || int64MapValue(t, row, "output_tokens_sum") != 200 || int64MapValue(t, row, "cached_tokens_sum") != 50 {
		t.Fatalf("unexpected token sums: %+v", row)
	}
	if int64MapValue(t, row, "reasoning_tokens_sum") != 30 || int64MapValue(t, row, "cache_read_tokens_sum") != 40 || int64MapValue(t, row, "cache_creation_tokens_sum") != 60 {
		t.Fatalf("unexpected detail token sums: %+v", row)
	}
	if int64MapValue(t, row, "total_tokens_sum") != 350 {
		t.Fatalf("unexpected total tokens: %+v", row)
	}
}

func TestMigrateAddsTokenBreakdownColumnsWithZeroDefaults(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE query_logs (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    ts INTEGER NOT NULL,
		    client_key_id INTEGER NOT NULL DEFAULT 0,
		    provider TEXT NOT NULL DEFAULT '',
		    auth_id TEXT NOT NULL DEFAULT '',
		    model TEXT NOT NULL DEFAULT '',
		    input_tokens INTEGER DEFAULT 0,
		    output_tokens INTEGER DEFAULT 0,
		    cached_tokens INTEGER DEFAULT 0,
		    total_tokens INTEGER DEFAULT 0,
		    success INTEGER DEFAULT 1
		);
		CREATE TABLE hourly_aggregates (
		    hour_ts INTEGER NOT NULL,
		    client_key_id INTEGER NOT NULL DEFAULT 0,
		    provider TEXT NOT NULL DEFAULT '',
		    auth_id TEXT NOT NULL DEFAULT '',
		    model TEXT NOT NULL DEFAULT '',
		    request_count INTEGER DEFAULT 0,
		    success_count INTEGER DEFAULT 0,
		    error_count INTEGER DEFAULT 0,
		    input_tokens_sum INTEGER DEFAULT 0,
		    output_tokens_sum INTEGER DEFAULT 0,
		    cached_tokens_sum INTEGER DEFAULT 0,
		    total_tokens_sum INTEGER DEFAULT 0,
		    PRIMARY KEY (hour_ts, client_key_id, provider, auth_id, model)
		);
		INSERT INTO query_logs (ts,client_key_id,provider,auth_id,model,input_tokens,output_tokens,cached_tokens,total_tokens,success)
		VALUES (1,1,'claude','auth-a','opus',10,20,30,60,1);
		INSERT INTO hourly_aggregates (hour_ts,client_key_id,provider,auth_id,model,request_count,success_count,error_count,input_tokens_sum,output_tokens_sum,cached_tokens_sum,total_tokens_sum)
		VALUES (0,1,'claude','auth-a','opus',1,1,0,10,20,30,60);
	`)
	if err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var reasoningTokens, cacheReadTokens, cacheCreationTokens int64
	if err := db.QueryRow(`SELECT reasoning_tokens, cache_read_tokens, cache_creation_tokens FROM query_logs`).Scan(&reasoningTokens, &cacheReadTokens, &cacheCreationTokens); err != nil {
		t.Fatalf("query migrated query_logs columns: %v", err)
	}
	if reasoningTokens != 0 || cacheReadTokens != 0 || cacheCreationTokens != 0 {
		t.Fatalf("unexpected query_logs defaults: %d %d %d", reasoningTokens, cacheReadTokens, cacheCreationTokens)
	}
	var reasoningSum, cacheReadSum, cacheCreationSum int64
	if err := db.QueryRow(`SELECT reasoning_tokens_sum, cache_read_tokens_sum, cache_creation_tokens_sum FROM hourly_aggregates`).Scan(&reasoningSum, &cacheReadSum, &cacheCreationSum); err != nil {
		t.Fatalf("query migrated hourly_aggregates columns: %v", err)
	}
	if reasoningSum != 0 || cacheReadSum != 0 || cacheCreationSum != 0 {
		t.Fatalf("unexpected hourly_aggregates defaults: %d %d %d", reasoningSum, cacheReadSum, cacheCreationSum)
	}
}

type testGinContext struct {
	values map[string]any
}

func (g testGinContext) Get(key string) (any, bool) {
	value, ok := g.values[key]
	return value, ok
}

func TestClientKeyIDFromContextAcceptsNumericTypes(t *testing.T) {
	if got := ClientKeyIDFromContext(context.WithValue(context.Background(), ClientKeyIDCtxKey, "12")); got != 12 {
		t.Fatalf("string client key ID = %d, want 12", got)
	}
	if got := ClientKeyIDFromContext(context.WithValue(context.Background(), ClientKeyIDCtxKey, int64(34))); got != 34 {
		t.Fatalf("int64 client key ID = %d, want 34", got)
	}
	if got := ClientKeyIDFromContext(context.WithValue(context.Background(), ClientKeyIDCtxKey, 56)); got != 56 {
		t.Fatalf("int client key ID = %d, want 56", got)
	}
}

func TestClientKeyIDFromContextFallsBackToGinMetadata(t *testing.T) {
	ginCtx := testGinContext{values: map[string]any{
		"accessMetadata": map[string]string{"api_key_id": "78"},
	}}
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	if got := ClientKeyIDFromContext(ctx); got != 78 {
		t.Fatalf("gin metadata client key ID = %d, want 78", got)
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

func TestProviderQuotaLinesTreatsOneAsOnePercent(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 21, 5, 0, 0, 0, time.UTC)
	store.InsertQuotaSnapshot(start.Unix(), "codex", "auth-plus", "code_five_hour", 1, 0)

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(5*time.Minute).Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 1 {
		t.Fatalf("unexpected series: %+v", resp)
	}
	if got := resp.Series[0].Points[0].QuotaUsedPercent; got != 1 {
		t.Fatalf("quota used percent = %v, want 1", got)
	}
}

func TestClassifyQuotaWindow(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"five_hour":                     "5h",
		"FIVE_HOUR":                     "5h",
		"primary_five_hour":             "5h",
		"code_five_hour":                "5h",
		"code_review_primary_five_hour": "5h",
		"additional_foo_five_hour":      "5h",
		"five-hour":                     "5h",
		"fivehour":                      "5h",
		"seven_day":                     "7d",
		"seven_day_oauth_apps":          "7d",
		"seven_day_opus":                "7d",
		"weekly":                        "7d",
		"code_weekly":                   "7d",
		"primary_weekly":                "7d",
		"gemini-2.5-pro:REQUESTS":       "",
		"daily-default":                 "",
		"iguana_necktie":                "",
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
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "five_hour", 50, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "seven_day", 12, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "codex", "auth-c", "code_weekly", 100, 0)
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
	if _, ok := got["codex|auth-c"]; ok {
		t.Fatalf("codex weekly-only auth should not appear in 5h class response: %+v", got)
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
	if got["codex|auth-c"] != "code_weekly" {
		t.Fatalf("codex with 7d class should pick code_weekly, got %q", got["codex|auth-c"])
	}
	if got["gemini-cli|auth-g"] != "gemini-2.5-pro:REQUESTS" {
		t.Fatalf("gemini fallback under 7d class should keep its model window, got %q", got["gemini-cli|auth-g"])
	}
}

func TestProviderQuotaLinesWindowClassUsesIndependentDisplayRange(t *testing.T) {
	store := newPricingTestStore(t)
	start := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "five_hour", 10, 0)
	store.InsertQuotaSnapshot(start.Add(48*time.Hour+10*time.Second).Unix(), "claude", "auth-a", "five_hour", 20, 0)
	store.InsertQuotaSnapshot(start.Add(10*time.Second).Unix(), "claude", "auth-a", "seven_day", 70, 0)

	end := start.Add(72 * time.Hour)
	resp, err := store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "5h")
	if err != nil {
		t.Fatalf("provider quota lines 5h over 3d: %v", err)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("expected 1 series, got %d: %+v", len(resp.Series), resp.Series)
	}
	series := resp.Series[0]
	if series.WindowType != "five_hour" {
		t.Fatalf("window type = %q, want five_hour", series.WindowType)
	}
	if len(series.Points) != 72 {
		t.Fatalf("5h window over 3d should return 72 hourly points, got %d", len(series.Points))
	}
	if series.Points[48].BucketTS != start.Add(48*time.Hour).Unix() || series.Points[48].QuotaUsedPercent != 20 {
		t.Fatalf("expected 48h point to use five_hour data, got %+v", series.Points[48])
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), end.Unix(), false, false, "7d")
	if err != nil {
		t.Fatalf("provider quota lines 7d over 3d: %v", err)
	}
	if len(resp.Series) != 1 || resp.Series[0].WindowType != "seven_day" {
		t.Fatalf("7d window over 3d should select seven_day series: %+v", resp.Series)
	}
}

func intMapValue(row map[string]any, key string) int {
	switch v := row[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func int64MapValue(t *testing.T, row map[string]any, key string) int64 {
	t.Helper()
	switch v := row[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		t.Fatalf("%s has unexpected type %T in %+v", key, row[key], row)
		return 0
	}
}

func near(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}
