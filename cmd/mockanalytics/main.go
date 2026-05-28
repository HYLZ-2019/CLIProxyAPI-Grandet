package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/analytics"

	_ "modernc.org/sqlite"
)

type scenarioClient struct {
	id   int
	name string
}

type scenarioModel struct {
	provider string
	authID   string
	model    string
	avgIn    int
	avgOut   int
	prices   [3]float64
}

var clients = []scenarioClient{
	{id: 1, name: "team-alpha"},
	{id: 2, name: "team-beta"},
	{id: 3, name: "personal"},
}

var models = []scenarioModel{
	{provider: "claude", authID: "personal@claude", model: "claude-opus-4-7", avgIn: 3200, avgOut: 900, prices: [3]float64{5, 25, 0.5}},
	{provider: "claude", authID: "personal@claude", model: "claude-sonnet-4-6", avgIn: 2400, avgOut: 750, prices: [3]float64{3, 15, 0.3}},
	{provider: "codex", authID: "team-alpha@codex", model: "gpt-5-codex", avgIn: 1900, avgOut: 650, prices: [3]float64{1.25, 10, 0.125}},
	{provider: "codex", authID: "team-alpha@codex", model: "gpt-5", avgIn: 1100, avgOut: 450, prices: [3]float64{1.25, 10, 0.125}},
	{provider: "gemini-cli", authID: "team-beta@gcli", model: "gemini-2.5-pro", avgIn: 3600, avgOut: 1100, prices: [3]float64{1.25, 10, 0.125}},
}

func main() {
	var dbPath string
	flag.StringVar(&dbPath, "db", "", "Path to analytics.db (required)")
	flag.Parse()
	if dbPath == "" {
		log.Fatal("missing -db flag")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}

	if err := analytics.Init(dbPath, 7); err != nil {
		log.Fatalf("init analytics: %v", err)
	}
	defer analytics.Shutdown()
	store := analytics.Get()

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, tbl := range []string{
		"query_logs", "hourly_aggregates", "quota_exhaustion_events", "quota_snapshots", "daily_token_prices",
	} {
		if _, err := db.Exec("DELETE FROM " + tbl); err != nil {
			log.Fatalf("delete %s: %v", tbl, err)
		}
	}

	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	hourlyPoints, err := seedUsage(store, rng, now)
	if err != nil {
		log.Fatalf("seed usage: %v", err)
	}
	if err := seedQuotaSnapshotsAndEvents(store, rng, now, hourlyPoints); err != nil {
		log.Fatalf("seed quota: %v", err)
	}
	for _, q := range []string{
		"SELECT COUNT(*) FROM query_logs",
		"SELECT COUNT(*) FROM hourly_aggregates",
		"SELECT COUNT(*) FROM quota_exhaustion_events",
		"SELECT COUNT(*) FROM quota_snapshots",
		"SELECT COUNT(*) FROM daily_token_prices",
	} {
		var n int
		_ = db.QueryRow(q).Scan(&n)
		fmt.Printf("%-50s %d rows\n", q, n)
	}
	fmt.Println("done.")
}

func seedUsage(store *analytics.Store, rng *rand.Rand, now time.Time) (map[string]map[int64]float64, error) {
	start := now.Add(-7 * 24 * time.Hour)
	hourlyPoints := map[string]map[int64]float64{}
	for t := start; t.Before(now); t = t.Add(time.Minute) {
		hour := t.Hour()
		base := 0.05
		switch {
		case hour >= 9 && hour <= 22:
			base = 0.42
		case hour >= 1 && hour <= 6:
			base = 0.02
		}
		if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
			base *= 0.55
		}
		if rng.Float64() > base {
			continue
		}
		client := clients[rng.Intn(len(clients))]
		model := models[rng.Intn(len(models))]
		success := rng.Float64() >= 0.04
		if model.provider == "codex" && hour >= 20 && hour < 24 && rng.Float64() < 0.22 {
			success = false
		}
		input := int64(model.avgIn + rng.Intn(max(1, model.avgIn/2)))
		output := int64(model.avgOut + rng.Intn(max(1, model.avgOut/2)))
		cached := int64(rng.Intn(max(1, int(input/4))))
		total := input + output + cached
		store.InsertQueryLog(t.Unix(), client.id, model.provider, model.authID, model.model, input, output, cached, total, success)
		hourTS := t.Unix() / 3600 * 3600
		store.UpsertHourlyAggregate(hourTS, client.id, model.provider, model.authID, model.model, input, output, cached, total, success)
		points := float64(input)/1_000_000*model.prices[0] + float64(output)/1_000_000*model.prices[1] + float64(cached)/1_000_000*model.prices[2]
		if hourlyPoints[model.provider] == nil {
			hourlyPoints[model.provider] = map[int64]float64{}
		}
		hourlyPoints[model.provider][hourTS] += points
	}
	return hourlyPoints, nil
}

func seedQuotaSnapshotsAndEvents(store *analytics.Store, rng *rand.Rand, now time.Time, hourlyPoints map[string]map[int64]float64) error {
	start := now.Add(-7 * 24 * time.Hour).Truncate(time.Hour)
	providerAuth := map[string]string{
		"claude":     "personal@claude",
		"codex":      "team-alpha@codex",
		"gemini-cli": "team-beta@gcli",
	}
	providerModel := map[string]string{
		"claude":     "claude-opus-4-7",
		"codex":      "gpt-5-codex",
		"gemini-cli": "gemini-2.5-pro",
	}
	used := map[string]float64{"claude": 12, "codex": 18, "gemini-cli": 8}
	lastWeek := map[string]time.Time{}
	for t := start; t.Before(now); t = t.Add(time.Hour) {
		for provider, authID := range providerAuth {
			week := startOfWeek(t)
			if lastWeek[provider].IsZero() {
				lastWeek[provider] = week
			} else if !week.Equal(lastWeek[provider]) {
				used[provider] = 2 + rng.Float64()*8
				lastWeek[provider] = week
			}
			used[provider] += hourlyPoints[provider][t.Unix()]/10000*100 + rng.Float64()*0.12
			if rng.Float64() < 0.03 {
				used[provider] += 0.4 + rng.Float64()*1.2
			}
			used[provider] = math.Min(99.5, used[provider])
			store.InsertQuotaSnapshot(t.Unix(), provider, authID, "weekly", used[provider], week.Add(7*24*time.Hour).Unix())
			if used[provider] > 88 && rng.Float64() < 0.28 {
				store.InsertQuotaEvent(t.Add(time.Duration(rng.Intn(55))*time.Minute).Unix(), provider, authID, providerModel[provider], week.Add(7*24*time.Hour).Unix())
			}
		}
	}
	for provider, authID := range providerAuth {
		for _, hoursAgo := range []int{18, 42, 90} {
			t := now.Add(-time.Duration(hoursAgo) * time.Hour)
			resetAt := startOfWeek(t).Add(7 * 24 * time.Hour).Unix()
			store.InsertQuotaEvent(t.Unix(), provider, authID, providerModel[provider], resetAt)
		}
	}
	return nil
}

func startOfWeek(t time.Time) time.Time {
	dow := int(t.Weekday())
	if dow == 0 {
		dow = 7
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return d.Add(-time.Duration(dow-1) * 24 * time.Hour)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
