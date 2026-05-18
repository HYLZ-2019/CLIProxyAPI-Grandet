package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// buildConfig returns a YAML config that wires the proxy to the given upstream
// and enables analytics. authDir is created inside the temp dir by the proxy.
func buildConfig(port int, mgmtKey, clientKey, upstreamURL string) string {
	return fmt.Sprintf(`port: %d
host: "127.0.0.1"
remote-management:
  allow-remote: false
  secret-key: %q
  disable-control-panel: true
auth-dir: "/tmp/cliproxyapi-integ-auth"
api-keys:
  - %q
debug: false
logging-to-file: false
analytics:
  enabled: true
  raw-log-retention-days: 7
openai-compatibility:
  - name: mock
    base-url: %q
    api-key-entries:
      - api-key: mock-upstream-key
    models:
      - name: mock-model
        alias: test-model
`, port, mgmtKey, clientKey, upstreamURL)
}

// postChat sends a chat completion request and returns the response.
func postChat(t *testing.T, proxyURL, clientKey string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, proxyURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+clientKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	return resp
}

func TestNonStreamingChat(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)

	resp := postChat(t, proxy.URL, "test-client-key", map[string]any{
		"model": "test-model",
		"messages": []any{
			map[string]string{"role": "user", "content": "Hello"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	choices, _ := result["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in response: %v", result)
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if content, _ := msg["content"].(string); !strings.Contains(content, "mock") {
		t.Errorf("unexpected content: %q", content)
	}

	reqs := upstream.Requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream got %d requests, want 1", len(reqs))
	}
	if reqs[0].Path != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions", reqs[0].Path)
	}
	if got := reqs[0].Headers.Get("Authorization"); got != "Bearer mock-upstream-key" {
		t.Errorf("upstream Authorization = %q, want Bearer mock-upstream-key", got)
	}
	// Verify the proxy forwarded the upstream model name, not the client alias.
	var upstreamBody map[string]any
	_ = json.Unmarshal(reqs[0].Body, &upstreamBody)
	if model, _ := upstreamBody["model"].(string); model != "mock-model" {
		t.Errorf("upstream model = %q, want mock-model (alias mapping broken)", model)
	}
}

func TestStreamingChat(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)

	resp := postChat(t, proxy.URL, "test-client-key", map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []any{
			map[string]string{"role": "user", "content": "Hello"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	text, _ := readSSEContent(resp.Body)
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "mock") {
		t.Errorf("streamed text = %q, want to contain 'Hello' and 'mock'", text)
	}
	// We don't assert on usage from the streamed final chunk: the proxy may strip
	// or translate it depending on the executor's protocol mapping. The
	// analytics test below covers token recording end-to-end.
}

func TestInvalidAuth(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)

	resp := postChat(t, proxy.URL, "wrong-key", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]string{"role": "user", "content": "x"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	if got := len(upstream.Requests()); got != 0 {
		t.Errorf("upstream got %d requests, want 0 (rejected before forwarding)", got)
	}
}

func TestModelsList(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)

	// waitForModel doubles as the assertion: it polls /v1/models and fatals if
	// the model never appears.
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)
}

func TestAnalyticsQueryLog(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)

	// Send two successful requests
	for i := 0; i < 2; i++ {
		resp := postChat(t, proxy.URL, "test-client-key", map[string]any{
			"model":    "test-model",
			"messages": []any{map[string]string{"role": "user", "content": "hi"}},
		})
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	db := analyticsDB(t, proxy.DataDir)

	// Wait for analytics writes to land (usage plugin is async).
	var rowCount int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rowCount = queryCount(t, db, `SELECT COUNT(*) FROM query_logs WHERE success = 1`)
		if rowCount >= 2 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if rowCount < 2 {
		t.Fatalf("query_logs has %d successful rows, want >= 2", rowCount)
	}

	// Verify tokens were recorded
	var inTok, outTok, totalTok int
	err := db.QueryRow(`SELECT
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(total_tokens), 0)
		FROM query_logs WHERE success = 1`).Scan(&inTok, &outTok, &totalTok)
	if err != nil {
		t.Fatalf("token sum query: %v", err)
	}
	if inTok == 0 && outTok == 0 && totalTok == 0 {
		t.Errorf("expected non-zero token counts, got in=%d out=%d total=%d", inTok, outTok, totalTok)
	}

	// Verify hourly aggregate also got at least one row.
	if got := queryCount(t, db, `SELECT COUNT(*) FROM hourly_aggregates`); got == 0 {
		t.Errorf("hourly_aggregates is empty, want >= 1 row")
	}
}

func TestQuotaEvent429(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)
	// Switch upstream to 429 mode after the proxy has registered the model,
	// so the model-readiness probe (which actually calls upstream during /v1/models?
	// no - /v1/models reads from the registry, not upstream) is not impacted.
	upstream.SetMode(mode429)

	resp := postChat(t, proxy.URL, "test-client-key", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	})
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	// We don't care what the proxy returned (likely 429 or 500 after retries),
	// just that the 429 from upstream was recorded as a quota event.

	db := analyticsDB(t, proxy.DataDir)
	var n int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n = queryCount(t, db, `SELECT COUNT(*) FROM quota_exhaustion_events`)
		if n >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if n == 0 {
		t.Errorf("quota_exhaustion_events is empty after upstream 429")
	}
}

func TestAnalyticsManagementAPI(t *testing.T) {
	upstream := newMockUpstream(t)
	port := freePort(t)
	cfg := buildConfig(port, "test-mgmt-secret", "test-client-key", upstream.URL)
	proxy := startProxy(t, port, cfg)
	waitForModel(t, proxy.URL, "test-client-key", "test-model", 15*time.Second)

	// Generate one successful request first
	resp := postChat(t, proxy.URL, "test-client-key", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	})
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Give analytics time to flush
	time.Sleep(500 * time.Millisecond)

	// Call the management summary endpoint
	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v0/management/analytics/summary", nil)
	req.Header.Set("Authorization", "Bearer test-mgmt-secret")
	mgmtResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mgmt summary: %v", err)
	}
	defer mgmtResp.Body.Close()
	if mgmtResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(mgmtResp.Body)
		t.Fatalf("mgmt summary status = %d: %s", mgmtResp.StatusCode, body)
	}

	// Call hourly aggregates
	req2, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v0/management/analytics/hourly", nil)
	req2.Header.Set("Authorization", "Bearer test-mgmt-secret")
	hourlyResp, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("mgmt hourly: %v", err)
	}
	defer hourlyResp.Body.Close()
	if hourlyResp.StatusCode != http.StatusOK {
		t.Errorf("mgmt hourly status = %d", hourlyResp.StatusCode)
	}

	// Call config endpoint and verify it reports enabled
	req3, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v0/management/analytics/config", nil)
	req3.Header.Set("Authorization", "Bearer test-mgmt-secret")
	cfgResp, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("mgmt config: %v", err)
	}
	defer cfgResp.Body.Close()
	var cfgBody map[string]any
	_ = json.NewDecoder(cfgResp.Body).Decode(&cfgBody)
	if enabled, _ := cfgBody["enabled"].(bool); !enabled {
		t.Errorf("analytics config enabled = %v, want true (body=%v)", enabled, cfgBody)
	}
}
