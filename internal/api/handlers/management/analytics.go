package management

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/analytics"
)

// GetAnalyticsSummary returns aggregate totals for the last 24 hours.
// Optional ?from=&to= override the window (Unix timestamps).
func (h *Handler) GetAnalyticsSummary(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	from, to := parseTimeRange(c, 24*time.Hour)
	row, err := store.Summary(from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, row)
}

// GetAnalyticsHourly returns hourly_aggregates rows for the last 7 days by default.
// Optional ?from=&to= override the window (Unix timestamps).
func (h *Handler) GetAnalyticsHourly(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	from, to := parseTimeRange(c, 7*24*time.Hour)
	rows, err := store.HourlyRows(from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []analytics.HourlyAggregateRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// GetAnalyticsByModel returns usage grouped by model for the last 24 hours.
func (h *Handler) GetAnalyticsByModel(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	from, to := parseTimeRange(c, 24*time.Hour)
	rows, err := store.ByModel(from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(http.StatusOK, rows)
}

// GetAnalyticsByClient returns usage grouped by client_key_id for the last 24 hours.
func (h *Handler) GetAnalyticsByClient(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	from, to := parseTimeRange(c, 24*time.Hour)
	rows, err := store.ByClient(from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(http.StatusOK, rows)
}

// GetAnalyticsQuotaEvents returns recent quota exhaustion (429) events.
// Optional ?limit= controls max rows (default 100).
func (h *Handler) GetAnalyticsQuotaEvents(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := store.QuotaEvents(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []analytics.QuotaEventRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// GetAnalyticsQuotaSnapshots returns recent active quota poll results.
// Optional ?provider= filters by provider; ?limit= controls max rows (default 200).
func (h *Handler) GetAnalyticsQuotaSnapshots(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	provider := c.Query("provider")
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := store.QuotaSnapshots(provider, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []analytics.QuotaSnapshotRow{}
	}
	c.JSON(http.StatusOK, rows)
}

func (h *Handler) GetAnalyticsTokenPrices(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	date := c.Query("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date"})
		return
	}
	rows, err := store.TokenPrices(date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []analytics.TokenPriceRow{}
	}
	c.JSON(http.StatusOK, rows)
}

func (h *Handler) PostAnalyticsSolveTokenPrices(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	dateText := c.Query("date")
	var date time.Time
	var err error
	if dateText == "" {
		date = time.Now()
	} else {
		date, err = time.Parse("2006-01-02", dateText)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date"})
			return
		}
	}
	resp, err := store.SolveTokenPricesForDateWithResult(date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp == nil {
		resp = &analytics.TokenPriceSolveResponse{PriceDate: date.Format("2006-01-02"), Status: "no_token_usage", Rows: []analytics.TokenPriceRow{}, Providers: []analytics.TokenPriceSolveProviderResult{}}
	}
	if resp.Rows == nil {
		resp.Rows = []analytics.TokenPriceRow{}
	}
	if resp.Providers == nil {
		resp.Providers = []analytics.TokenPriceSolveProviderResult{}
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetAnalyticsProviderQuotaLines(c *gin.Context) {
	store := analytics.Get()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analytics not enabled"})
		return
	}
	from, to := parseTimeRange(c, 7*24*time.Hour)
	resetOn429 := c.Query("reset_on_429") == "true" || c.Query("reset_on_429") == "1"
	resetOnRefresh := c.Query("reset_on_refresh") == "true" || c.Query("reset_on_refresh") == "1"
	windowClass := ""
	switch c.Query("window") {
	case "5h", "7d":
		windowClass = c.Query("window")
	}
	resp, err := store.ProviderQuotaLines(from, to, resetOn429, resetOnRefresh, windowClass)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp == nil {
		resp = &analytics.ProviderQuotaLinesResponse{}
	}
	if resp.Series == nil {
		resp.Series = []analytics.ProviderQuotaSeries{}
	}
	c.JSON(http.StatusOK, resp)
}

// GetAnalyticsConfig returns the current analytics configuration.
func (h *Handler) GetAnalyticsConfig(c *gin.Context) {
	h.mu.Lock()
	cfg := h.cfg
	h.mu.Unlock()
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	c.JSON(http.StatusOK, cfg.Analytics)
}

// PutAnalyticsConfig updates the analytics configuration and persists it.
// Body: {"enabled": bool, "raw-log-retention-days": int}
func (h *Handler) PutAnalyticsConfig(c *gin.Context) {
	var body struct {
		Enabled             *bool `json:"enabled"`
		RawLogRetentionDays *int  `json:"raw-log-retention-days"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	h.mu.Lock()
	if body.Enabled != nil {
		h.cfg.Analytics.Enabled = *body.Enabled
	}
	if body.RawLogRetentionDays != nil && *body.RawLogRetentionDays >= 1 {
		h.cfg.Analytics.RawLogRetentionDays = *body.RawLogRetentionDays
	}
	retentionDays := h.cfg.Analytics.RawLogRetentionDays
	enabled := h.cfg.Analytics.Enabled
	dbPath := h.analyticsDBPath
	authManager := h.authManager
	h.mu.Unlock()

	if enabled && dbPath != "" {
		if store := analytics.Get(); store != nil {
			store.SetRetention(retentionDays)
		} else {
			if err := analytics.Init(dbPath, retentionDays); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			analytics.StartQuotaPoller(context.Background(), analytics.Get(), authManager)
		}
	} else if !enabled {
		if err := analytics.Shutdown(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	h.persist(c)
}

// --- helpers ---

func parseTimeRange(c *gin.Context, defaultWindow time.Duration) (from, to int64) {
	now := time.Now()
	to = now.Unix()
	from = now.Add(-defaultWindow).Unix()

	if v := c.Query("from"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = t
		}
	}
	return from, to
}
