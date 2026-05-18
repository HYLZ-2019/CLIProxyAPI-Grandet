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

func TestSolveTokenPricesForDateCleanData(t *testing.T) {
	store := newPricingTestStore(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start := date.Add(-24 * time.Hour)
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

	if err := store.SolveTokenPricesForDate(date); err != nil {
		t.Fatalf("solve prices: %v", err)
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

	equations, err := store.buildQuotaEquations("codex", "weekly", []TokenDimension{{Provider: "codex", Model: "gpt", TokenType: "input"}}, start.Unix(), start.Add(4*time.Hour).Unix())
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
	if err := store.UpsertTokenPrice(TokenPriceRow{PriceDate: time.Now().Format("2006-01-02"), Provider: "gemini", Model: "pro", TokenType: "input", PricePointsPerMillion: &price, Status: "solved"}); err != nil {
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

	resp, err := store.ProviderQuotaLines(start.Unix(), start.Add(4*time.Hour).Unix(), false, false)
	if err != nil {
		t.Fatalf("provider lines: %v", err)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 4 {
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

	resp, err = store.ProviderQuotaLines(start.Unix(), start.Add(4*time.Hour).Unix(), true, false)
	if err != nil {
		t.Fatalf("provider lines with 429 reset: %v", err)
	}
	points = resp.Series[0].Points
	if points[0].CLIProxyCumulativePoints != 100 || points[1].CLIProxyCumulativePoints != 100 || points[2].CLIProxyCumulativePoints != 100 {
		t.Fatalf("unexpected reset-on-429 cumulative points: %+v", points)
	}

	resp, err = store.ProviderQuotaLines(start.Unix(), start.Add(4*time.Hour).Unix(), false, true)
	if err != nil {
		t.Fatalf("provider lines with refresh reset: %v", err)
	}
	points = resp.Series[0].Points
	if points[0].CLIProxyCumulativePoints != 100 || points[1].CLIProxyCumulativePoints != 200 || points[2].CLIProxyCumulativePoints != 100 || points[3].CLIProxyCumulativePoints != 100 {
		t.Fatalf("unexpected reset-on-refresh cumulative points: %+v", points)
	}
}
