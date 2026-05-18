// Package integration runs black-box tests against a real CLIProxyAPI binary.
//
// TestMain builds the binary once. Each test spins up its own mock upstream and
// proxy subprocess, so tests are fully isolated.
//
// Run from repo root:
//
//	go test -v -timeout 180s ./test/integration/...
package integration

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// proxyBinary is the path to the compiled binary. Set once in TestMain.
var proxyBinary string

func TestMain(m *testing.M) {
	bin, err := buildBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: build proxy binary: %v\n", err)
		os.Exit(1)
	}
	proxyBinary = bin
	code := m.Run()
	_ = os.Remove(bin)
	os.Exit(code)
}

func buildBinary() (string, error) {
	root := repoRoot()
	bin := filepath.Join(os.TempDir(), fmt.Sprintf("cliproxyapi-test-%d", os.Getpid()))
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/server/")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out)
	}
	return bin, nil
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

type proxyInstance struct {
	URL     string
	DataDir string
	cmd     *exec.Cmd
	logBuf  *safeBuffer
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func startProxy(t *testing.T, port int, yamlConfig string) *proxyInstance {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlConfig), 0o644); err != nil {
		t.Fatalf("startProxy: write config: %v", err)
	}

	logBuf := &safeBuffer{}
	cmd := exec.Command(proxyBinary, "-config", cfgPath)
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("startProxy: exec.Start: %v", err)
	}

	inst := &proxyInstance{
		URL:     fmt.Sprintf("http://127.0.0.1:%d", port),
		DataDir: dir,
		cmd:     cmd,
		logBuf:  logBuf,
	}

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		if t.Failed() {
			t.Logf("=== proxy log ===\n%s", logBuf.String())
		}
	})

	if !waitReady(inst.URL+"/healthz", 20*time.Second) {
		t.Logf("proxy log:\n%s", logBuf.String())
		t.Fatalf("proxy at %s not ready after 20s", inst.URL)
	}
	return inst
}

func waitReady(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return true
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitForModel polls /v1/models until the given model id is listed or timeout
// elapses. /healthz passes as soon as the HTTP server binds, but auth/model
// registration happens asynchronously after that, so tests must wait for the
// model to actually be routable.
func waitForModel(t *testing.T, proxyURL, clientKey, modelID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	var lastBody string
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+clientKey)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			lastBody = string(body)
			var parsed map[string]any
			if json.Unmarshal(body, &parsed) == nil {
				data, _ := parsed["data"].([]any)
				for _, item := range data {
					if m, _ := item.(map[string]any); m != nil {
						if id, _ := m["id"].(string); id == modelID {
							return
						}
					}
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("model %q not registered after %v; last /v1/models body: %s", modelID, timeout, lastBody)
}

// analyticsDB opens the analytics SQLite DB. Retries briefly because writes are
// asynchronous and the file may not exist immediately after the first request.
func analyticsDB(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(dataDir, "data", "analytics.db")
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dbPath); err == nil {
			db, openErr := sql.Open("sqlite", dbPath+"?mode=ro")
			if openErr != nil {
				lastErr = openErr
			} else if pingErr := db.Ping(); pingErr != nil {
				_ = db.Close()
				lastErr = pingErr
			} else {
				t.Cleanup(func() { _ = db.Close() })
				return db
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("analyticsDB: %s not ready: %v", dbPath, lastErr)
	return nil
}

// queryCount runs SELECT COUNT(*) and returns the result.
func queryCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("queryCount %q: %v", q, err)
	}
	return n
}

// readSSEContent concatenates the assistant content fields from an SSE stream.
// Returns the assembled text and the final usage map (if present).
func readSSEContent(r io.Reader) (string, map[string]any) {
	var text strings.Builder
	var usage map[string]any
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if u, ok := chunk["usage"].(map[string]any); ok {
			usage = u
		}
		choices, _ := chunk["choices"].([]any)
		for _, c := range choices {
			cm, _ := c.(map[string]any)
			delta, _ := cm["delta"].(map[string]any)
			if content, ok := delta["content"].(string); ok {
				text.WriteString(content)
			}
		}
	}
	return text.String(), usage
}
