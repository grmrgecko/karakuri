package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRetryClassificationThroughEngine(t *testing.T) {
	for _, tc := range []struct {
		name, method, mode, statuses, body string
		status, requests                   int
	}{
		{name: "rate limited", method: "GET", status: 429, requests: 2},
		{name: "permanent client error", method: "GET", status: 400, requests: 1},
		{name: "command default", method: "POST", status: 503, requests: 1},
		{name: "command opt in", method: "POST", mode: "all", status: 503, requests: 2},
		{name: "disabled", method: "GET", mode: "none", status: 503, requests: 1},
		{name: "selected status", method: "GET", statuses: "409", status: 409, requests: 2},
		{name: "unselected status", method: "GET", statuses: "409", status: 503, requests: 1},
		{name: "bad response format", method: "GET", status: 200, body: "not json", requests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n == 1 {
					w.Header().Set("Retry-After", "2")
					w.WriteHeader(tc.status)
				}
				if tc.body != "" {
					fmt.Fprint(w, tc.body)
				} else {
					fmt.Fprint(w, `{}`)
				}
			}))
			defer srv.Close()
			clock := engine.NewFakeClock(time.Now())
			reg := engine.NewRegistry()
			RegisterAll(reg)
			eng := engine.New(engine.Options{Registry: reg, Clock: clock})
			eng.Start()
			defer eng.Stop()
			cfg := cfgJSON(t, map[string]any{"url": srv.URL, "method": tc.method, "retry_mode": tc.mode, "retry_statuses": tc.statuses})
			resultCh := make(chan *engine.NodeTestResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := eng.TestNode(context.Background(), engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "http", Type: "http-request", Config: cfg, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 10, MaxDelayMs: 100}}}}, StartNodeID: "http", SeedInputs: map[string]*engine.Msg{"in": {Payload: "original"}}})
				errCh <- err
				resultCh <- result
			}()
			if tc.requests == 2 {
				require.True(t, clock.BlockUntilAfterCalls(1, time.Second))
				assert.Equal(t, 100*time.Millisecond, clock.AfterCalls()[0], "Retry-After must obey the configured cap")
				clock.Advance(100 * time.Millisecond)
			}
			select {
			case result := <-resultCh:
				require.NoError(t, <-errCh)
				if tc.requests == 2 {
					assert.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
				} else {
					assert.Equal(t, engine.RunStatusFailed, result.Status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP node did not settle")
			}
			assert.Equal(t, int32(tc.requests), calls.Load())
		})
	}
}

func TestHTTPFailureDetailsAreBoundedAndRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.Header().Set("Set-Cookie", "session=secret")
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(503)
		fmt.Fprint(w, strings.Repeat("x", maxErrorBodyBytes+10))
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(map[string]any{"url": strings.Replace(srv.URL, "http://", "http://user:secret@", 1) + "/?token=secret", "auth": "query", "auth_name": "key", "token": "other-secret"})
	_, err := HTTPRequestNode{}.Execute(context.Background(), newTestCtx(string(cfg), nil), map[string]*engine.Msg{"in": {}})
	var failure *engine.ExecutionError
	require.ErrorAs(t, err, &failure)
	assert.True(t, failure.Retryable)
	assert.Equal(t, int64(3000), failure.RetryAfterMS)
	assert.NotContains(t, failure.Message, "secret")
	assert.Equal(t, srv.URL+"/", failure.Details["url"])
	headers := failure.Details["headers"].(map[string]string)
	assert.Equal(t, "req-1", headers["X-Request-Id"])
	assert.NotContains(t, headers, "Set-Cookie")
	assert.Len(t, failure.Details["body"], maxErrorBodyBytes)
	assert.Equal(t, true, failure.Details["body_truncated"])
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, value string
		want        int64
	}{
		{"seconds", "5", 5000}, {"date", now.Add(5 * time.Second).Format(http.TimeFormat), 5000}, {"past", now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"signed", "+5", 0}, {"negative", "-1", 0}, {"overflow", "18446744073709551615", 0}, {"bad", "tomorrow", 0},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, parseRetryAfter(tc.value, now)) })
	}
}

func TestRetryAfterDateRoundsUpToMinimum(t *testing.T) {
	now := time.Date(2026, 10, 6, 20, 0, 0, 1, time.UTC)
	deadline := now.Truncate(time.Second).Add(time.Second)
	assert.Equal(t, int64(1000), parseRetryAfter(deadline.Format(http.TimeFormat), now), "rounding down would retry before the HTTP date")
}

func TestHTTPTransportPolicyAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.(http.Hijacker)
		conn, _, err := h.Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer srv.Close()
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			cfg := cfgJSON(t, map[string]any{"url": srv.URL, "method": method})
			_, err := HTTPRequestNode{}.Execute(context.Background(), newTestCtx(string(cfg), nil), map[string]*engine.Msg{"in": {}})
			var failure *engine.ExecutionError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, method == "GET", failure.Retryable)
		})
	}
	clock := engine.NewFakeClock(time.Now())
	reg := engine.NewRegistry()
	RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg, Clock: clock})
	eng.Start()
	defer eng.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *engine.NodeTestResult, 1)
	cfg := cfgJSON(t, map[string]any{"url": srv.URL})
	go func() {
		result, _ := eng.TestNode(ctx, engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "http", Type: "http-request", Config: cfg, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 60000}}}}, StartNodeID: "http", SeedInputs: map[string]*engine.Msg{"in": {}}})
		done <- result
	}()
	require.True(t, clock.BlockUntilAfterCalls(1, time.Second))
	cancel()
	select {
	case result := <-done:
		require.NotNil(t, result)
		assert.Equal(t, engine.RunStatusCanceled, result.Status)
	case <-time.After(time.Second):
		t.Fatal("cancellation must interrupt Retry-After/backoff")
	}
}
