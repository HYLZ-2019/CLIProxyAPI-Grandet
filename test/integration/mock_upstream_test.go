package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mockMode controls how the mock upstream responds to chat completions requests.
type mockMode int

const (
	modeSuccess mockMode = iota
	mode429
	mode500
)

// capturedRequest records what the upstream actually received from the proxy.
type capturedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

// MockUpstream is an OpenAI-compatible mock that captures incoming requests
// and returns deterministic responses.
type MockUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
	mode     mockMode
}

func newMockUpstream(t *testing.T) *MockUpstream {
	m := &MockUpstream{}
	m.Server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.Close)
	return m
}

func (m *MockUpstream) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, capturedRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    body,
	})
	mode := m.mode
	m.mu.Unlock()

	switch mode {
	case mode429:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
		return
	case mode500:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "internal error"},
		})
		return
	}

	var req map[string]any
	_ = json.Unmarshal(body, &req)
	if stream, _ := req["stream"].(bool); stream {
		m.writeStreaming(w, req)
		return
	}
	m.writeNonStreaming(w, req)
}

func (m *MockUpstream) writeNonStreaming(w http.ResponseWriter, req map[string]any) {
	model, _ := req["model"].(string)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": "Hello from mock!"},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     11,
			"completion_tokens": 4,
			"total_tokens":      15,
		},
	})
}

func (m *MockUpstream) writeStreaming(w http.ResponseWriter, req map[string]any) {
	model, _ := req["model"].(string)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	emit := func(payload map[string]any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// First chunk: role
	emit(map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"role": "assistant", "content": ""},
		}},
	})
	for _, piece := range []string{"Hello", " from", " mock!"} {
		emit(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": piece},
			}},
		})
	}
	// Final chunk with usage + stop
	emit(map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens": 11, "completion_tokens": 3, "total_tokens": 14,
		},
	})
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (m *MockUpstream) Requests() []capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]capturedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func (m *MockUpstream) SetMode(mode mockMode) {
	m.mu.Lock()
	m.mode = mode
	m.mu.Unlock()
}

func (m *MockUpstream) Reset() {
	m.mu.Lock()
	m.requests = nil
	m.mode = modeSuccess
	m.mu.Unlock()
}
