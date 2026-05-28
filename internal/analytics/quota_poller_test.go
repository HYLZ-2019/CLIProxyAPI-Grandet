package analytics

import (
	"testing"
	"time"
)

type quotaSnapshotTestRow struct {
	provider    string
	authID      string
	windowType  string
	usedPercent float64
	resetAt     int64
}

func quotaSnapshotRows(t *testing.T, store *Store) []quotaSnapshotTestRow {
	t.Helper()
	rows, err := store.db.Query(`
		SELECT provider, auth_id, window_type, used_percent, reset_at
		FROM quota_snapshots
		ORDER BY window_type`)
	if err != nil {
		t.Fatalf("query quota snapshots: %v", err)
	}
	defer rows.Close()

	out := []quotaSnapshotTestRow{}
	for rows.Next() {
		var row quotaSnapshotTestRow
		if err := rows.Scan(&row.provider, &row.authID, &row.windowType, &row.usedPercent, &row.resetAt); err != nil {
			t.Fatalf("scan quota snapshot: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate quota snapshots: %v", err)
	}
	return out
}

func findQuotaSnapshot(rows []quotaSnapshotTestRow, windowType string) (quotaSnapshotTestRow, bool) {
	for _, row := range rows {
		if row.windowType == windowType {
			return row, true
		}
	}
	return quotaSnapshotTestRow{}, false
}

func TestNextQuotaPollBoundaryAlignsWithFiveMinuteBuckets(t *testing.T) {
	if int64(quotaPollInterval/time.Second) != analyticsFiveMinuteBucketSeconds {
		t.Fatalf("quota poll interval should match fine bucket seconds")
	}

	cases := []struct {
		now  time.Time
		want time.Time
	}{
		{
			now:  time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC),
			want: time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC),
		},
		{
			now:  time.Date(2026, 5, 18, 8, 0, 1, 0, time.UTC),
			want: time.Date(2026, 5, 18, 8, 5, 0, 0, time.UTC),
		},
		{
			now:  time.Date(2026, 5, 18, 8, 4, 59, int(time.Second-time.Nanosecond), time.UTC),
			want: time.Date(2026, 5, 18, 8, 5, 0, 0, time.UTC),
		},
		{
			now:  time.Date(2026, 5, 18, 8, 5, 0, 1, time.UTC),
			want: time.Date(2026, 5, 18, 8, 10, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		got := nextQuotaPollBoundary(tc.now)
		if !got.Equal(tc.want) {
			t.Fatalf("nextQuotaPollBoundary(%s) = %s, want %s", tc.now, got, tc.want)
		}
		if got.Unix()%analyticsFiveMinuteBucketSeconds != 0 {
			t.Fatalf("poll boundary %s is not aligned to %d seconds", got, analyticsFiveMinuteBucketSeconds)
		}
	}
}

func TestCaptureClaudeQuotaSnapshotFromAPIResponse(t *testing.T) {
	store := newPricingTestStore(t)
	ts := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC).Unix()
	resetAt := time.Date(2026, 5, 18, 12, 34, 56, 0, time.UTC).Unix()
	body := []byte(`{
		"five_hour": {"utilization": 12.5, "resets_at": "2026-05-18T12:34:56Z"},
		"seven_day": {"utilization": 64}
	}`)

	captured := CaptureQuotaSnapshotFromAPIResponse(store, ts, "claude", "auth-claude", "GET", "https://api.anthropic.com/api/oauth/usage", body)
	if !captured {
		t.Fatal("expected capture to succeed")
	}

	rows := quotaSnapshotRows(t, store)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	fiveHour, ok := findQuotaSnapshot(rows, "five_hour")
	if !ok {
		t.Fatalf("missing five_hour row: %+v", rows)
	}
	if fiveHour.provider != "claude" || fiveHour.authID != "auth-claude" || fiveHour.usedPercent != 12.5 || fiveHour.resetAt != resetAt {
		t.Fatalf("unexpected five_hour row: %+v", fiveHour)
	}
	sevenDay, ok := findQuotaSnapshot(rows, "seven_day")
	if !ok || sevenDay.usedPercent != 64 {
		t.Fatalf("unexpected seven_day row: %+v", sevenDay)
	}
}

func TestCaptureCodexLegacyQuotaSnapshotFromAPIResponse(t *testing.T) {
	store := newPricingTestStore(t)
	body := []byte(`{
		"windows": [
			{"window_type": "5h", "used_percent": 33.3, "reset_at": 1800000000},
			{"window_type": "weekly", "used_percent": 44.4}
		]
	}`)

	captured := CaptureQuotaSnapshotFromAPIResponse(store, 100, "codex", "auth-codex", "GET", "https://chatgpt.com/backend-api/wham/usage", body)
	if !captured {
		t.Fatal("expected capture to succeed")
	}

	rows := quotaSnapshotRows(t, store)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	fiveHour, ok := findQuotaSnapshot(rows, "5h")
	if !ok || fiveHour.usedPercent != 33.3 || fiveHour.resetAt != 1800000000 {
		t.Fatalf("unexpected 5h row: %+v", fiveHour)
	}
}

func TestCaptureCodexNestedQuotaSnapshotFromAPIResponse(t *testing.T) {
	store := newPricingTestStore(t)
	body := []byte(`{
		"rate_limit": {
			"primary_window": {"limit_window_seconds": 18000, "used_percent": 10},
			"secondary_window": {"limit_window_seconds": 604800, "used_percent": 20}
		},
		"code_review_rate_limit": {
			"primary_window": {"limit_window_seconds": 18000, "used_percent": 30}
		}
	}`)

	captured := CaptureQuotaSnapshotFromAPIResponse(store, 100, "codex", "auth-codex", "GET", "https://chatgpt.com/backend-api/wham/usage", body)
	if !captured {
		t.Fatal("expected capture to succeed")
	}

	rows := quotaSnapshotRows(t, store)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(rows), rows)
	}
	for windowType, used := range map[string]float64{
		"code_five_hour":        10,
		"code_weekly":           20,
		"code_review_five_hour": 30,
	} {
		row, ok := findQuotaSnapshot(rows, windowType)
		if !ok || row.usedPercent != used {
			t.Fatalf("unexpected row for %s: %+v", windowType, row)
		}
	}
}

func TestCaptureGeminiCLIQuotaSnapshotFromAPIResponse(t *testing.T) {
	store := newPricingTestStore(t)
	body := []byte(`{
		"buckets": [
			{"modelId": "gemini-2.5-pro", "tokenType": "input", "remainingFraction": 0.25, "resetTime": "2026-05-18T12:00:00Z"},
			{"model_id": "gemini-2.5-flash", "remaining_amount": 0, "reset_time": "2026-05-18T13:00:00Z"}
		]
	}`)

	captured := CaptureQuotaSnapshotFromAPIResponse(store, 100, "gemini-cli", "auth-gemini", "POST", "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota", body)
	if !captured {
		t.Fatal("expected capture to succeed")
	}

	rows := quotaSnapshotRows(t, store)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	pro, ok := findQuotaSnapshot(rows, "gemini-2.5-pro:input")
	if !ok || pro.usedPercent != 75 {
		t.Fatalf("unexpected pro row: %+v", pro)
	}
	flash, ok := findQuotaSnapshot(rows, "gemini-2.5-flash")
	if !ok || flash.usedPercent != 100 {
		t.Fatalf("unexpected flash row: %+v", flash)
	}
}

func TestCaptureAntigravityQuotaSnapshotFromAPIResponse(t *testing.T) {
	store := newPricingTestStore(t)
	body := []byte(`{
		"models": {
			"claude-sonnet": {"quotaInfo": {"remainingFraction": 0.8, "resetTime": "2026-05-18T12:00:00Z"}},
			"gpt-5": {"quota_info": {"reset_time": "2026-05-18T13:00:00Z"}}
		}
	}`)

	captured := CaptureQuotaSnapshotFromAPIResponse(store, 100, "antigravity", "auth-ag", "POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", body)
	if !captured {
		t.Fatal("expected capture to succeed")
	}

	rows := quotaSnapshotRows(t, store)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	sonnet, ok := findQuotaSnapshot(rows, "claude-sonnet")
	if !ok || sonnet.usedPercent < 19.99 || sonnet.usedPercent > 20.01 {
		t.Fatalf("unexpected sonnet row: %+v", sonnet)
	}
	gpt, ok := findQuotaSnapshot(rows, "gpt-5")
	if !ok || gpt.usedPercent != 100 {
		t.Fatalf("unexpected gpt row: %+v", gpt)
	}
}

func TestCaptureQuotaSnapshotIgnoresUnsupportedAndBadResponses(t *testing.T) {
	store := newPricingTestStore(t)
	if CaptureQuotaSnapshotFromAPIResponse(store, 100, "claude", "auth", "GET", "https://example.com/usage", []byte(`{"used_percent": 1}`)) {
		t.Fatal("expected unsupported URL to be ignored")
	}
	if CaptureQuotaSnapshotFromAPIResponse(store, 100, "claude", "auth", "GET", "https://api.anthropic.com/api/oauth/usage", []byte(`not-json`)) {
		t.Fatal("expected bad JSON to be ignored")
	}
	rows := quotaSnapshotRows(t, store)
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %+v", rows)
	}
}
