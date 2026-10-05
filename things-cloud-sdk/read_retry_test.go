// These tests exercise rate-limit retry limits, response ownership, and cancellation.
// Read retries use immediate server guidance so the suite does not sleep through backoff delays.
package thingscloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type retryRoundTripper func(*http.Request) (*http.Response, error)

func (f retryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type retryBody struct {
	io.Reader
	closed bool
}

func (b *retryBody) Close() error { b.closed = true; return nil }

func TestReadRateLimitRetries(t *testing.T) {
	for _, tc := range []struct {
		name, method, retryAfter string
		statuses                 []int
		wantCalls                int
	}{
		{"GET recovers", http.MethodGet, "0", []int{429, 429, 200}, 3},
		{"HEAD recovers", http.MethodHead, "0", []int{429, 200}, 2},
		{"bounded attempts", http.MethodGet, "0", []int{429, 429, 429, 429, 200}, 4},
		{"long delay", http.MethodGet, "60", []int{429, 200}, 1},
		{"POST not retried", http.MethodPost, "0", []int{429, 200}, 1},
		{"DELETE not retried", http.MethodDelete, "0", []int{429, 200}, 1},
		{"PUT not retried", http.MethodPut, "0", []int{429, 200}, 1},
		{"server failure not retried", http.MethodGet, "0", []int{503, 200}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New("https://example.test", "test@example.test", "test-password", WithRequestInterval(0))
			var bodies []*retryBody
			c.client.Transport = retryRoundTripper(func(r *http.Request) (*http.Response, error) {
				index := len(bodies)
				if index >= len(tc.statuses) {
					t.Fatal("unexpected additional retry")
				}
				body := &retryBody{Reader: strings.NewReader("response")}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: tc.statuses[index], Status: http.StatusText(tc.statuses[index]), Header: http.Header{"Retry-After": {tc.retryAfter}}, Body: body}, nil
			})
			req, err := http.NewRequest(tc.method, "https://example.test/history", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.do(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(bodies) != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", len(bodies), tc.wantCalls)
			}
			for i, body := range bodies {
				if body.closed != (i < len(bodies)-1) {
					t.Fatalf("body %d closed = %v", i, body.closed)
				}
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				apiErr := newAPIError(resp)
				if apiErr.RetryAfter != tc.retryAfter || !strings.Contains(apiErr.Error(), "Retry-After") {
					t.Fatalf("rate limit guidance lost: %v", apiErr)
				}
			}
			resp.Body.Close()
		})
	}
}

func TestReadRetryDoesNotReplayTransportFailure(t *testing.T) {
	c := New("https://example.test", "test@example.test", "test-password", WithRequestInterval(0))
	want := errors.New("connection failed")
	calls := 0
	c.client.Transport = retryRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, want
	})
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/history", nil)
	if _, err := c.do(req); !errors.Is(err, want) || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestReadRetryCancellationClosesResponse(t *testing.T) {
	c := New("https://example.test", "test@example.test", "test-password", WithRequestInterval(0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &retryBody{Reader: strings.NewReader("rate limited")}
	calls := 0
	c.client.Transport = retryRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"1"}}, Body: body}, nil
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test/history", nil)
	if _, err := c.do(req); !errors.Is(err, context.Canceled) || calls != 1 || !body.closed {
		t.Fatalf("calls = %d, closed = %v, error = %v", calls, body.closed, err)
	}
}

func TestReadRetryDelay(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"0", 0},
		{" 2 ", 2 * time.Second},
		{now.Add(4 * time.Second).Format(http.TimeFormat), 4 * time.Second},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		if got := readRetryDelay(tc.header, 0, now); got != tc.want {
			t.Fatalf("delay for %q = %v, want %v", tc.header, got, tc.want)
		}
	}
	for _, header := range []string{"60", "18446744073709551616", now.Add(time.Hour).Format(http.TimeFormat)} {
		if got := readRetryDelay(header, 0, now); got <= maxReadRetryWait {
			t.Fatalf("long delay %q shortened to %v", header, got)
		}
	}
	for attempt := 0; attempt < maxReadRetries; attempt++ {
		base := 500 * time.Millisecond << attempt
		for _, header := range []string{"", "invalid", "-1"} {
			if got := readRetryDelay(header, attempt, now); got < base || got >= base+250*time.Millisecond {
				t.Fatalf("fallback delay = %v, want [%v, %v)", got, base, base+250*time.Millisecond)
			}
		}
	}
}

func TestOutdatedAncestorErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status   int
		response string
		want     bool
	}{
		{409, "OutdatedAncestor", true}, {409, "Other", false}, {429, "OutdatedAncestor", false},
	} {
		e := &APIError{StatusCode: tc.status, ThingsResponse: tc.response}
		if got := e.IsOutdatedAncestor(); got != tc.want {
			t.Fatalf("%+v classification = %v", e, got)
		}
	}
}
