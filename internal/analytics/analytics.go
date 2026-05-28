// Package analytics provides per-request usage logging and quota tracking backed by SQLite.
package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type contextKey string

// ClientKeyIDCtxKey is the context key for the authenticated client API key ID.
const ClientKeyIDCtxKey = contextKey("analytics_client_key_id")

const defaultRetentionDays = 7

const (
	analyticsFiveMinuteBucketSeconds = int64(5 * 60)
	analyticsHourBucketSeconds       = int64(60 * 60)
	analyticsFineBucketMaxRange      = int64(24 * 60 * 60)
)

func analyticsBucketSeconds(fromTS, toTS int64) int64 {
	if toTS-fromTS <= analyticsFineBucketMaxRange {
		return analyticsFiveMinuteBucketSeconds
	}
	return analyticsHourBucketSeconds
}

func floorBucket(ts, bucketSeconds int64) int64 {
	if bucketSeconds <= 0 {
		bucketSeconds = analyticsHourBucketSeconds
	}
	return ts - ts%bucketSeconds
}

// Store wraps the analytics SQLite database.
type Store struct {
	db                  *sql.DB
	dbPath              string
	rawRetentionSeconds int64
	mu                  sync.RWMutex
	cleanupStop         chan struct{}
	closeOnce           sync.Once
}

var (
	globalMu    sync.RWMutex
	globalStore *Store
)

// Init opens (or creates) the SQLite database at dbPath, runs schema migrations,
// and starts the background cleanup goroutine. Calling Init again replaces the existing store.
func Init(dbPath string, retentionDays int) error {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("analytics: create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("analytics: open db: %w", err)
	}
	// SQLite WAL mode tolerates concurrent reads but only one writer at a time.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("analytics: ping db: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("analytics: migrate: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath, cleanupStop: make(chan struct{})}
	s.SetRetention(retentionDays)

	globalMu.Lock()
	old := globalStore
	globalStore = s
	globalMu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	s.startCleanup()
	return nil
}

// Get returns the global Store, or nil if analytics has not been initialized.
func Get() *Store {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalStore
}

// SetRetention updates the raw-log retention window.
func (s *Store) SetRetention(days int) {
	if days <= 0 {
		days = defaultRetentionDays
	}
	s.mu.Lock()
	s.rawRetentionSeconds = int64(days) * 86400
	s.mu.Unlock()
}

// DBPath returns the file path of this database.
func (s *Store) DBPath() string {
	if s == nil {
		return ""
	}
	return s.dbPath
}

// Close shuts down the database.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		if s.cleanupStop != nil {
			close(s.cleanupStop)
		}
		if s.db != nil {
			err = s.db.Close()
		}
	})
	return err
}

func Shutdown() error {
	globalMu.Lock()
	old := globalStore
	globalStore = nil
	globalMu.Unlock()
	if old == nil {
		return nil
	}
	return old.Close()
}

// --- Schema ---

const schema = `
CREATE TABLE IF NOT EXISTS query_logs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,
    client_key_id   INTEGER NOT NULL DEFAULT 0,
    provider        TEXT    NOT NULL DEFAULT '',
    auth_id         TEXT    NOT NULL DEFAULT '',
    model           TEXT    NOT NULL DEFAULT '',
    input_tokens    INTEGER DEFAULT 0,
    output_tokens   INTEGER DEFAULT 0,
    cached_tokens   INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    cache_read_tokens INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    total_tokens    INTEGER DEFAULT 0,
    success         INTEGER DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_ql_ts ON query_logs(ts);
CREATE INDEX IF NOT EXISTS idx_ql_provider_ts ON query_logs(provider,ts);

CREATE TABLE IF NOT EXISTS hourly_aggregates (
    hour_ts           INTEGER NOT NULL,
    client_key_id     INTEGER NOT NULL DEFAULT 0,
    provider          TEXT    NOT NULL DEFAULT '',
    auth_id           TEXT    NOT NULL DEFAULT '',
    model             TEXT    NOT NULL DEFAULT '',
    request_count     INTEGER DEFAULT 0,
    success_count     INTEGER DEFAULT 0,
    error_count       INTEGER DEFAULT 0,
    input_tokens_sum  INTEGER DEFAULT 0,
    output_tokens_sum INTEGER DEFAULT 0,
    cached_tokens_sum INTEGER DEFAULT 0,
    reasoning_tokens_sum INTEGER DEFAULT 0,
    cache_read_tokens_sum INTEGER DEFAULT 0,
    cache_creation_tokens_sum INTEGER DEFAULT 0,
    total_tokens_sum  INTEGER DEFAULT 0,
    PRIMARY KEY (hour_ts, client_key_id, provider, auth_id, model)
);
CREATE INDEX IF NOT EXISTS idx_ha_provider_hour ON hourly_aggregates(provider,hour_ts);

CREATE TABLE IF NOT EXISTS quota_exhaustion_events (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts       INTEGER NOT NULL,
    provider TEXT    NOT NULL DEFAULT '',
    auth_id  TEXT    NOT NULL DEFAULT '',
    model    TEXT    NOT NULL DEFAULT '',
    reset_at INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_qe_ts ON quota_exhaustion_events(ts);
CREATE INDEX IF NOT EXISTS idx_qe_provider_ts ON quota_exhaustion_events(provider,ts);

CREATE TABLE IF NOT EXISTS quota_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,
    provider     TEXT    NOT NULL DEFAULT '',
    auth_id      TEXT    NOT NULL DEFAULT '',
    window_type  TEXT    DEFAULT '',
    used_percent REAL    DEFAULT 0,
    reset_at     INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_qs_ts ON quota_snapshots(ts);
CREATE INDEX IF NOT EXISTS idx_qs_provider_window_ts ON quota_snapshots(provider,window_type,ts);

CREATE TABLE IF NOT EXISTS daily_token_prices (
    price_date                 TEXT NOT NULL,
    provider                   TEXT NOT NULL,
    auth_id                    TEXT NOT NULL DEFAULT '',
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
    PRIMARY KEY (price_date, provider, auth_id, model, token_type)
);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := ensureColumn(db, "query_logs", "auth_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []string{"reasoning_tokens", "cache_read_tokens", "cache_creation_tokens"} {
		if err := ensureColumn(db, "query_logs", column, "INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	if err := ensureColumn(db, "quota_exhaustion_events", "reset_at", "INTEGER DEFAULT 0"); err != nil {
		return err
	}
	if err := migrateHourlyAggregates(db); err != nil {
		return err
	}
	for _, column := range []string{"reasoning_tokens_sum", "cache_read_tokens_sum", "cache_creation_tokens_sum"} {
		if err := ensureColumn(db, "hourly_aggregates", column, "INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	return migrateDailyTokenPrices(db)
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}

func tablePrimaryKeyColumns(db *sql.DB, table string) (map[string]int, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]int{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = pk
	}
	return cols, rows.Err()
}

func migrateHourlyAggregates(db *sql.DB) error {
	pkCols, err := tablePrimaryKeyColumns(db, "hourly_aggregates")
	if err != nil {
		return err
	}
	needsRebuild := pkCols["auth_id"] == 0

	if !needsRebuild {
		_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ha_provider_hour ON hourly_aggregates(provider,hour_ts)`)
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS hourly_aggregates_new (
		    hour_ts           INTEGER NOT NULL,
		    client_key_id     INTEGER NOT NULL DEFAULT 0,
		    provider          TEXT    NOT NULL DEFAULT '',
		    auth_id           TEXT    NOT NULL DEFAULT '',
		    model             TEXT    NOT NULL DEFAULT '',
		    request_count     INTEGER DEFAULT 0,
		    success_count     INTEGER DEFAULT 0,
		    error_count       INTEGER DEFAULT 0,
		    input_tokens_sum  INTEGER DEFAULT 0,
		    output_tokens_sum INTEGER DEFAULT 0,
		    cached_tokens_sum INTEGER DEFAULT 0,
		    total_tokens_sum  INTEGER DEFAULT 0,
		    PRIMARY KEY (hour_ts, client_key_id, provider, auth_id, model)
		);
		INSERT OR IGNORE INTO hourly_aggregates_new
		(hour_ts,client_key_id,provider,auth_id,model,request_count,success_count,error_count,
		 input_tokens_sum,output_tokens_sum,cached_tokens_sum,total_tokens_sum)
		SELECT hour_ts,client_key_id,provider,'',model,request_count,success_count,error_count,
		       input_tokens_sum,output_tokens_sum,cached_tokens_sum,total_tokens_sum
		FROM hourly_aggregates;
		DROP TABLE IF EXISTS hourly_aggregates;
		ALTER TABLE hourly_aggregates_new RENAME TO hourly_aggregates;
		CREATE INDEX IF NOT EXISTS idx_ha_provider_hour ON hourly_aggregates(provider,hour_ts);
	`)
	return err
}

func migrateDailyTokenPrices(db *sql.DB) error {
	pkCols, err := tablePrimaryKeyColumns(db, "daily_token_prices")
	if err != nil {
		return err
	}
	needsRebuild := pkCols["auth_id"] == 0

	if !needsRebuild {
		_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_dtp_date_provider_auth ON daily_token_prices(price_date,provider,auth_id)`)
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS daily_token_prices_new (
		    price_date                 TEXT NOT NULL,
		    provider                   TEXT NOT NULL,
		    auth_id                    TEXT NOT NULL DEFAULT '',
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
		    PRIMARY KEY (price_date, provider, auth_id, model, token_type)
		);
		INSERT OR REPLACE INTO daily_token_prices_new
		(price_date,provider,auth_id,model,token_type,price_points_per_million,status,equation_count,
		 residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at)
		SELECT price_date,provider,'',model,token_type,price_points_per_million,status,equation_count,
		       residual_rms,residual_mad,source_from_ts,source_to_ts,solved_at
		FROM daily_token_prices;
		DROP TABLE IF EXISTS daily_token_prices;
		ALTER TABLE daily_token_prices_new RENAME TO daily_token_prices;
		CREATE INDEX IF NOT EXISTS idx_dtp_date_provider_auth ON daily_token_prices(price_date,provider,auth_id);
	`)
	return err
}

// --- Context helpers ---

// ClientKeyIDFromContext reads the client API key numeric ID from ctx.
func ClientKeyIDFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	switch v := ctx.Value(ClientKeyIDCtxKey).(type) {
	case string:
		id, _ := strconv.Atoi(v)
		return id
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	}
	return clientKeyIDFromGinContext(ctx)
}

func clientKeyIDFromGinContext(ctx context.Context) int {
	ginCtx, ok := ctx.Value("gin").(ginContextReader)
	if !ok || ginCtx == nil {
		return 0
	}
	value, exists := ginCtx.Get("accessMetadata")
	if !exists {
		return 0
	}
	switch metadata := value.(type) {
	case map[string]string:
		id, _ := strconv.Atoi(metadata["api_key_id"])
		return id
	case map[string]any:
		id, _ := strconv.Atoi(fmt.Sprintf("%v", metadata["api_key_id"]))
		return id
	default:
		return 0
	}
}

// --- Write methods ---

// InsertQueryLog records a single proxied request.
func (s *Store) InsertQueryLog(ts int64, clientKeyID int, provider, authID, model string,
	inputTokens, outputTokens, cachedTokens, _ int64, success bool, detailedTokens ...int64) {
	if s == nil {
		return
	}
	reasoningTokens, cacheReadTokens, cacheCreationTokens := unpackDetailedTokens(detailedTokens)
	succ := 1
	if !success {
		succ = 0
	}
	computedTotal := inputTokens + outputTokens + cachedTokens
	_, _ = s.db.Exec(
		`INSERT INTO query_logs
		 (ts,client_key_id,provider,auth_id,model,input_tokens,output_tokens,cached_tokens,reasoning_tokens,cache_read_tokens,cache_creation_tokens,total_tokens,success)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ts, clientKeyID, provider, authID, model,
		inputTokens, outputTokens, cachedTokens, reasoningTokens, cacheReadTokens, cacheCreationTokens, computedTotal, succ)
}

// UpsertHourlyAggregate merges one request into the hourly rollup.
func (s *Store) UpsertHourlyAggregate(hourTS int64, clientKeyID int, provider, authID, model string,
	inputTokens, outputTokens, cachedTokens, _ int64, success bool, detailedTokens ...int64) {
	if s == nil {
		return
	}
	reasoningTokens, cacheReadTokens, cacheCreationTokens := unpackDetailedTokens(detailedTokens)
	sc, ec := int64(0), int64(0)
	if success {
		sc = 1
	} else {
		ec = 1
	}
	computedTotal := inputTokens + outputTokens + cachedTokens
	_, _ = s.db.Exec(
		`INSERT INTO hourly_aggregates
		 (hour_ts,client_key_id,provider,auth_id,model,
		  request_count,success_count,error_count,
		  input_tokens_sum,output_tokens_sum,cached_tokens_sum,reasoning_tokens_sum,cache_read_tokens_sum,cache_creation_tokens_sum,total_tokens_sum)
		 VALUES (?,?,?,?,?,1,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(hour_ts,client_key_id,provider,auth_id,model) DO UPDATE SET
		   request_count             = request_count             + 1,
		   success_count             = success_count             + excluded.success_count,
		   error_count               = error_count               + excluded.error_count,
		   input_tokens_sum          = input_tokens_sum          + excluded.input_tokens_sum,
		   output_tokens_sum         = output_tokens_sum         + excluded.output_tokens_sum,
		   cached_tokens_sum         = cached_tokens_sum         + excluded.cached_tokens_sum,
		   reasoning_tokens_sum      = reasoning_tokens_sum      + excluded.reasoning_tokens_sum,
		   cache_read_tokens_sum     = cache_read_tokens_sum     + excluded.cache_read_tokens_sum,
		   cache_creation_tokens_sum = cache_creation_tokens_sum + excluded.cache_creation_tokens_sum,
		   total_tokens_sum          = total_tokens_sum          + excluded.total_tokens_sum`,
		hourTS, clientKeyID, provider, authID, model,
		sc, ec,
		inputTokens, outputTokens, cachedTokens, reasoningTokens, cacheReadTokens, cacheCreationTokens, computedTotal)
}

func unpackDetailedTokens(values []int64) (int64, int64, int64) {
	var reasoningTokens, cacheReadTokens, cacheCreationTokens int64
	if len(values) > 0 {
		reasoningTokens = values[0]
	}
	if len(values) > 1 {
		cacheReadTokens = values[1]
	}
	if len(values) > 2 {
		cacheCreationTokens = values[2]
	}
	return reasoningTokens, cacheReadTokens, cacheCreationTokens
}

// InsertQuotaEvent records a 429 quota-exhaustion event.
func (s *Store) InsertQuotaEvent(ts int64, provider, authID, model string, resetAt ...int64) {
	if s == nil {
		return
	}
	var reset int64
	if len(resetAt) > 0 {
		reset = resetAt[0]
	}
	_, _ = s.db.Exec(
		`INSERT INTO quota_exhaustion_events (ts,provider,auth_id,model,reset_at) VALUES (?,?,?,?,?)`,
		ts, provider, authID, model, reset)
}

// InsertQuotaSnapshot records the result of an active quota poll.
func (s *Store) InsertQuotaSnapshot(ts int64, provider, authID, windowType string, usedPercent float64, resetAt int64) {
	if s == nil {
		return
	}
	_, _ = s.db.Exec(
		`INSERT INTO quota_snapshots (ts,provider,auth_id,window_type,used_percent,reset_at)
		 VALUES (?,?,?,?,?,?)`,
		ts, provider, authID, windowType, usedPercent, resetAt)
}

func (s *Store) latestQuotaResetAt(provider, authID string, afterTS int64) int64 {
	if s == nil {
		return 0
	}
	var resetAt int64
	err := s.db.QueryRow(`
		SELECT reset_at FROM quota_snapshots
		WHERE provider = ? AND (? = '' OR auth_id = ?) AND reset_at > ?
		ORDER BY ts DESC LIMIT 1`, provider, authID, authID, afterTS).Scan(&resetAt)
	if err != nil {
		return 0
	}
	return resetAt
}

// --- Query types & methods ---

// SummaryRow holds aggregate totals for a time window.
type SummaryRow struct {
	Requests            int64 `json:"requests"`
	SuccessCount        int64 `json:"success_count"`
	ErrorCount          int64 `json:"error_count"`
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

// HourlyAggregateRow mirrors one row in hourly_aggregates.
type HourlyAggregateRow struct {
	HourTS                 int64  `json:"hour_ts"`
	BucketTS               int64  `json:"bucket_ts,omitempty"`
	BucketSeconds          int64  `json:"bucket_seconds,omitempty"`
	ClientKeyID            int    `json:"client_key_id"`
	Provider               string `json:"provider"`
	AuthID                 string `json:"auth_id"`
	Model                  string `json:"model"`
	RequestCount           int64  `json:"request_count"`
	SuccessCount           int64  `json:"success_count"`
	ErrorCount             int64  `json:"error_count"`
	InputTokensSum         int64  `json:"input_tokens_sum"`
	OutputTokensSum        int64  `json:"output_tokens_sum"`
	CachedTokensSum        int64  `json:"cached_tokens_sum"`
	ReasoningTokensSum     int64  `json:"reasoning_tokens_sum"`
	CacheReadTokensSum     int64  `json:"cache_read_tokens_sum"`
	CacheCreationTokensSum int64  `json:"cache_creation_tokens_sum"`
	TotalTokensSum         int64  `json:"total_tokens_sum"`
}

// QuotaEventRow mirrors one row in quota_exhaustion_events.
type QuotaEventRow struct {
	ID       int64  `json:"id"`
	TS       int64  `json:"ts"`
	Provider string `json:"provider"`
	AuthID   string `json:"auth_id"`
	Model    string `json:"model"`
	ResetAt  int64  `json:"reset_at"`
}

// QuotaSnapshotRow mirrors one row in quota_snapshots.
type QuotaSnapshotRow struct {
	ID          int64   `json:"id"`
	TS          int64   `json:"ts"`
	Provider    string  `json:"provider"`
	AuthID      string  `json:"auth_id"`
	WindowType  string  `json:"window_type"`
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

// Summary returns aggregate totals from query_logs for [fromTS, toTS).
func (s *Store) Summary(fromTS, toTS int64) (*SummaryRow, error) {
	if s == nil {
		return &SummaryRow{}, nil
	}
	row := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(success),0),
		       COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(reasoning_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_creation_tokens),0),
		       COALESCE(SUM(input_tokens),0) + COALESCE(SUM(output_tokens),0) + COALESCE(SUM(cached_tokens),0)
		FROM query_logs WHERE ts >= ? AND ts < ?`, fromTS, toTS)
	var r SummaryRow
	if err := row.Scan(&r.Requests, &r.SuccessCount, &r.ErrorCount,
		&r.InputTokens, &r.OutputTokens, &r.CachedTokens,
		&r.ReasoningTokens, &r.CacheReadTokens, &r.CacheCreationTokens, &r.TotalTokens); err != nil {
		return &SummaryRow{}, nil
	}
	return &r, nil
}

// HourlyRows returns usage aggregate rows for [fromTS, toTS), newest first.
func (s *Store) HourlyRows(fromTS, toTS int64) ([]HourlyAggregateRow, error) {
	if s == nil {
		return nil, nil
	}
	bucketSeconds := analyticsBucketSeconds(fromTS, toTS)
	if bucketSeconds == analyticsFiveMinuteBucketSeconds {
		return s.queryLogBucketRows(fromTS, toTS, bucketSeconds)
	}
	return s.hourlyAggregateBucketRows(fromTS, toTS, bucketSeconds)
}

func (s *Store) queryLogBucketRows(fromTS, toTS, bucketSeconds int64) ([]HourlyAggregateRow, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket_ts,client_key_id,provider,auth_id,model,
		       COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cached_tokens),0),COALESCE(SUM(reasoning_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_creation_tokens),0),
		       COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0)+COALESCE(SUM(cached_tokens),0)
		FROM query_logs WHERE ts >= ? AND ts < ?
		GROUP BY bucket_ts,client_key_id,provider,auth_id,model
		ORDER BY bucket_ts DESC`, bucketSeconds, bucketSeconds, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAggregateRows(rows, bucketSeconds)
}

func (s *Store) hourlyAggregateBucketRows(fromTS, toTS, bucketSeconds int64) ([]HourlyAggregateRow, error) {
	rows, err := s.db.Query(`
		SELECT hour_ts,client_key_id,provider,auth_id,model,
		       request_count,success_count,error_count,
		       input_tokens_sum,output_tokens_sum,cached_tokens_sum,
		       reasoning_tokens_sum,cache_read_tokens_sum,cache_creation_tokens_sum,
		       input_tokens_sum+output_tokens_sum+cached_tokens_sum
		FROM hourly_aggregates WHERE hour_ts >= ? AND hour_ts < ?
		ORDER BY hour_ts DESC`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAggregateRows(rows, bucketSeconds)
}

func scanAggregateRows(rows *sql.Rows, bucketSeconds int64) ([]HourlyAggregateRow, error) {
	var out []HourlyAggregateRow
	for rows.Next() {
		var r HourlyAggregateRow
		if err := rows.Scan(&r.HourTS, &r.ClientKeyID, &r.Provider, &r.AuthID, &r.Model,
			&r.RequestCount, &r.SuccessCount, &r.ErrorCount,
			&r.InputTokensSum, &r.OutputTokensSum, &r.CachedTokensSum,
			&r.ReasoningTokensSum, &r.CacheReadTokensSum, &r.CacheCreationTokensSum,
			&r.TotalTokensSum); err != nil {
			continue
		}
		r.BucketTS = r.HourTS
		r.BucketSeconds = bucketSeconds
		out = append(out, r)
	}
	return out, rows.Err()
}

// ByModel returns usage summed by (model, provider) for [fromTS, toTS).
func (s *Store) ByModel(fromTS, toTS int64) ([]map[string]any, error) {
	if s == nil {
		return nil, nil
	}
	if toTS-fromTS <= analyticsFineBucketMaxRange {
		return s.byModelFromQueryLogs(fromTS, toTS)
	}
	return s.byModelFromHourlyAggregates(fromTS, toTS)
}

func (s *Store) byModelFromQueryLogs(fromTS, toTS int64) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT model,provider,
		       COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cached_tokens),0),COALESCE(SUM(reasoning_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_creation_tokens),0),
		       COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0)+COALESCE(SUM(cached_tokens),0)
		FROM query_logs WHERE ts >= ? AND ts < ?
		GROUP BY model,provider ORDER BY COUNT(*) DESC`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanByModelRows(rows)
}

func (s *Store) byModelFromHourlyAggregates(fromTS, toTS int64) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT model,provider,
		       SUM(request_count),SUM(success_count),SUM(error_count),
		       SUM(input_tokens_sum),SUM(output_tokens_sum),
		       SUM(cached_tokens_sum),SUM(reasoning_tokens_sum),
		       SUM(cache_read_tokens_sum),SUM(cache_creation_tokens_sum),
		       SUM(input_tokens_sum)+SUM(output_tokens_sum)+SUM(cached_tokens_sum)
		FROM hourly_aggregates WHERE hour_ts >= ? AND hour_ts < ?
		GROUP BY model,provider ORDER BY SUM(request_count) DESC`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanByModelRows(rows)
}

func scanByModelRows(rows *sql.Rows) ([]map[string]any, error) {
	var out []map[string]any
	for rows.Next() {
		var model, provider string
		var req, succ, errC, inp, outp, cached, reasoning, cacheRead, cacheCreation, total int64
		if err := rows.Scan(&model, &provider, &req, &succ, &errC, &inp, &outp, &cached, &reasoning, &cacheRead, &cacheCreation, &total); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"model": model, "provider": provider,
			"request_count": req, "success_count": succ, "error_count": errC,
			"input_tokens_sum": inp, "output_tokens_sum": outp,
			"cached_tokens_sum": cached, "reasoning_tokens_sum": reasoning,
			"cache_read_tokens_sum": cacheRead, "cache_creation_tokens_sum": cacheCreation,
			"total_tokens_sum": total,
		})
	}
	return out, rows.Err()
}

// ByClient returns usage summed by client_key_id for [fromTS, toTS).
func (s *Store) ByClient(fromTS, toTS int64) ([]map[string]any, error) {
	if s == nil {
		return nil, nil
	}
	if toTS-fromTS <= analyticsFineBucketMaxRange {
		return s.byClientFromQueryLogs(fromTS, toTS)
	}
	return s.byClientFromHourlyAggregates(fromTS, toTS)
}

func (s *Store) byClientFromQueryLogs(fromTS, toTS int64) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT client_key_id,
		       COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0)+COALESCE(SUM(cached_tokens),0)
		FROM query_logs WHERE ts >= ? AND ts < ?
		GROUP BY client_key_id ORDER BY COUNT(*) DESC`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanByClientRows(rows)
}

func (s *Store) byClientFromHourlyAggregates(fromTS, toTS int64) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT client_key_id,
		       SUM(request_count),SUM(success_count),SUM(error_count),
		       SUM(input_tokens_sum),SUM(output_tokens_sum),
		       SUM(cached_tokens_sum),SUM(input_tokens_sum)+SUM(output_tokens_sum)+SUM(cached_tokens_sum)
		FROM hourly_aggregates WHERE hour_ts >= ? AND hour_ts < ?
		GROUP BY client_key_id ORDER BY SUM(request_count) DESC`, fromTS, toTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanByClientRows(rows)
}

func scanByClientRows(rows *sql.Rows) ([]map[string]any, error) {
	var out []map[string]any
	for rows.Next() {
		var clientKeyID int
		var req, succ, errC, inp, outp, cached, total int64
		if err := rows.Scan(&clientKeyID, &req, &succ, &errC, &inp, &outp, &cached, &total); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"client_key_id": clientKeyID,
			"request_count": req, "success_count": succ, "error_count": errC,
			"input_tokens_sum": inp, "output_tokens_sum": outp,
			"cached_tokens_sum": cached, "total_tokens_sum": total,
		})
	}
	return out, rows.Err()
}

// QuotaEvents returns the most recent quota exhaustion events.
func (s *Store) QuotaEvents(limit int) ([]QuotaEventRow, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id,ts,provider,auth_id,model,reset_at FROM quota_exhaustion_events
		 ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaEventRow
	for rows.Next() {
		var r QuotaEventRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Provider, &r.AuthID, &r.Model, &r.ResetAt); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// QuotaSnapshots returns recent quota snapshots, optionally filtered by provider.
func (s *Store) QuotaSnapshots(provider string, limit int) ([]QuotaSnapshotRow, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(provider) == "" {
		rows, err = s.db.Query(
			`SELECT id,ts,provider,auth_id,window_type,used_percent,reset_at
			 FROM quota_snapshots ORDER BY ts DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT id,ts,provider,auth_id,window_type,used_percent,reset_at
			 FROM quota_snapshots WHERE provider=? ORDER BY ts DESC LIMIT ?`, provider, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaSnapshotRow
	for rows.Next() {
		var r QuotaSnapshotRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Provider, &r.AuthID, &r.WindowType, &r.UsedPercent, &r.ResetAt); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
