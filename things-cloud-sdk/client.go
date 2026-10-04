// This file configures Things Cloud HTTP requests and handles bounded retries for rate-limited reads.
package thingscloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	// APIEndpoint is the public culturedcode https endpoint
	APIEndpoint        = "https://cloud.culturedcode.com"
	defaultHTTPTimeout = 30 * time.Second
	maxReadRetries     = 3
	maxReadRetryWait   = 15 * time.Second
)

var (
	// ErrUnauthorized is returned by the API when the credentials are wrong
	ErrUnauthorized = errors.New("unauthorized")
)

// APIError represents a non-OK HTTP response from Things Cloud,
// including the status code and the things-response header if present.
type APIError struct {
	StatusCode     int
	Status         string
	ThingsResponse string // value of the "things-response" header, e.g. "AbusePrevention"
	RetryAfter     string // server's Retry-After guidance for a rate-limited response
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("things cloud: %s", e.Status)
	if e.ThingsResponse != "" {
		message += fmt.Sprintf(" (things-response: %s)", e.ThingsResponse)
	}
	if e.StatusCode == http.StatusTooManyRequests {
		if e.RetryAfter != "" {
			message += fmt.Sprintf("; retry later according to Retry-After: %q", e.RetryAfter)
		} else {
			message += "; retry later"
		}
	}
	return message
}

// IsAbusePrevention reports whether the error is a Things Cloud abuse prevention block.
func (e *APIError) IsAbusePrevention() bool {
	return e.StatusCode == http.StatusTooManyRequests && e.ThingsResponse == "AbusePrevention"
}

// IsOutdatedAncestor reports a stale history cursor rejected by Things Cloud.
func (e *APIError) IsOutdatedAncestor() bool {
	return e.StatusCode == http.StatusConflict && e.ThingsResponse == "OutdatedAncestor"
}

// newAPIError creates an APIError from an HTTP response.
func newAPIError(resp *http.Response) *APIError {
	return &APIError{
		StatusCode:     resp.StatusCode,
		Status:         resp.Status,
		ThingsResponse: resp.Header.Get("Things-Response"),
		RetryAfter:     resp.Header.Get("Retry-After"),
	}
}

// ClientInfo represents the device metadata sent in the things-client-info header.
type ClientInfo struct {
	DeviceModel string `json:"dm"`
	LocalRegion string `json:"lr"`
	NF          bool   `json:"nf"`
	NK          bool   `json:"nk"`
	AppName     string `json:"nn"`
	AppVersion  string `json:"nv"`
	OSName      string `json:"on"`
	OSVersion   string `json:"ov"`
	PrimaryLang string `json:"pl"`
	UserLocale  string `json:"ul"`
}

// DefaultClientInfo returns a ClientInfo with default values matching a typical Mac client.
func DefaultClientInfo() ClientInfo {
	return ClientInfo{
		DeviceModel: "MacBookPro18,3",
		LocalRegion: "US",
		NF:          true,
		NK:          true,
		AppName:     "ThingsMac",
		AppVersion:  "32209501",
		OSName:      "macOS",
		OSVersion:   "15.7.3",
		PrimaryLang: "en-US",
		UserLocale:  "en-Latn-US",
	}
}

// Client is a culturedcode cloud client. It can be used to interact with the
// things cloud to manage your data.
type Client struct {
	Endpoint   string
	EMail      string
	password   string
	ClientInfo ClientInfo
	Debug      bool

	client      *http.Client
	rateLimiter *rate.Limiter
	common      service

	Accounts *AccountService
}

// ClientOption allows customizing the things client before it is returned.
type ClientOption func(*Client)

// WithProxy configures the HTTP client to use the provided proxy URL.
func WithProxy(proxyURL *url.URL) ClientOption {
	return func(c *Client) {
		if c.client == nil {
			c.client = &http.Client{}
		}
		c.client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}
}

// WithRequestInterval overrides the default one-request-per-second limiter.
// A non-positive interval disables throttling and is intended for isolated
// tests; production callers should keep the conservative default.
func WithRequestInterval(interval time.Duration) ClientOption {
	return func(c *Client) {
		if interval <= 0 {
			c.rateLimiter = rate.NewLimiter(rate.Inf, 1)
			return
		}
		c.rateLimiter = rate.NewLimiter(rate.Every(interval), 1)
	}
}

type service struct {
	client *Client
}

// New initializes a things client
func New(endpoint, email, password string, opts ...ClientOption) *Client {
	c := &Client{
		Endpoint:    endpoint,
		EMail:       email,
		password:    password,
		ClientInfo:  DefaultClientInfo(),
		rateLimiter: rate.NewLimiter(rate.Every(time.Second), 1),
		client:      &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	c.common.client = c
	c.Accounts = (*AccountService)(&c.common)
	return c
}

// ThingsUserAgent is the http user-agent header set by things for mac
const ThingsUserAgent = "ThingsMac/32209501"

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if err := c.rateLimiter.Wait(req.Context()); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}

	if req.Host == "" {
		uri := fmt.Sprintf("%s%s", c.Endpoint, req.URL)
		u, err := url.Parse(uri)
		if err != nil {
			return nil, err
		}
		req.URL = u
	}

	// Common headers matching Things.app
	req.Header.Set("Host", "cloud.culturedcode.com")
	req.Header.Set("User-Agent", ThingsUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Charset", "UTF-8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Only set Content-Type/Encoding for requests with body (POST, PUT, etc.)
	if req.Method != "GET" && req.Method != "HEAD" && req.Method != "DELETE" {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		req.Header.Set("Content-Encoding", "UTF-8")
	}

	ciJSON, err := json.Marshal(c.ClientInfo)
	if err != nil {
		return nil, fmt.Errorf("marshaling client info: %w", err)
	}
	req.Header.Set("Things-Client-Info", base64.StdEncoding.EncodeToString(ciJSON))

	if c.Debug {
		bs, _ := httputil.DumpRequest(req, true)
		log.Println("REQUEST:", string(bs))
	}

	canRetry := (req.Method == http.MethodGet || req.Method == http.MethodHead) && (req.Body == nil || req.Body == http.NoBody)
	var retryStarted time.Time
	var waited time.Duration
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := c.rateLimiter.Wait(req.Context()); err != nil {
				return nil, fmt.Errorf("rate limit wait: %w", err)
			}
		}
		resp, err := c.client.Do(req)
		if c.Debug {
			if err == nil {
				bs, _ := httputil.DumpResponse(resp, true)
				log.Println("RESPONSE:", string(bs))
			}
			log.Println()
		}
		if err != nil || !canRetry || resp.StatusCode != http.StatusTooManyRequests || attempt >= maxReadRetries {
			return resp, err
		}
		if retryStarted.IsZero() {
			retryStarted = time.Now()
		}
		delay := readRetryDelay(resp.Header.Get("Retry-After"), attempt, time.Now())
		// Return the rate limit response intact when the server's requested wait
		// exceeds our budget; retrying early would ignore its backoff guidance.
		if delay > maxReadRetryWait-waited || delay > maxReadRetryWait-time.Since(retryStarted) {
			return resp, nil
		}
		resp.Body.Close()
		if err := waitForReadRetry(req.Context(), delay); err != nil {
			return nil, fmt.Errorf("rate limit retry wait: %w", err)
		}
		waited += delay
	}
}

// readRetryDelay honors valid Retry-After values and otherwise uses exponential backoff with jitter.
func readRetryDelay(header string, attempt int, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	digits := header != ""
	for _, ch := range header {
		if ch < '0' || ch > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(header, 10, 64)
		if err != nil || seconds > uint64(maxReadRetryWait/time.Second) {
			return maxReadRetryWait + time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil {
		if delay := date.Sub(now); delay > 0 {
			return delay
		}
		return 0
	}
	return (500 * time.Millisecond << attempt) + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
}

// waitForReadRetry allows request cancellation to interrupt the backoff delay.
func waitForReadRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
