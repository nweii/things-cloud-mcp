// Commit validation rejects malformed IDs before HTTP and preserves uncertain outcomes.
package thingscloud

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHistoryWriteRejectsInvalidIdentifiersBeforeHTTP(t *testing.T) {
	for _, id := range []string{"", "task-1", "00010203-0405-4607-8809-0a0b0c0d0e0f", "2drXXUnFzwcLwp75NEn2"} {
		t.Run(id, func(t *testing.T) {
			calls := 0
			c := New("https://example.test", "test@example.com", "password", WithRequestInterval(0))
			c.client.Transport = retryRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"server-head-index":1}`))}, nil
			})
			h := &History{Client: c, ID: "history", LatestSchemaVersion: 301}
			if err := h.Write(testIdentifiable{ID: id}); err == nil || calls != 0 {
				t.Fatalf("invalid ID reached HTTP: err=%v calls=%d", err, calls)
			}
		})
	}
}

type failingCommitBody struct{}

func (failingCommitBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingCommitBody) Close() error             { return nil }

func TestHistoryWriteBodyReadFailureIsUncertain(t *testing.T) {
	calls := 0
	c := New("https://example.test", "test@example.com", "password", WithRequestInterval(0))
	c.client.Transport = retryRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: failingCommitBody{}}, nil
	})
	h := &History{Client: c, ID: "history", LatestServerIndex: 3, LatestSchemaVersion: 301}
	err := h.Write(testIdentifiable{ID: NewUUID()})
	var uncertain *CommitUncertainError
	if !errors.As(err, &uncertain) || h.LatestServerIndex != 3 || calls != 1 {
		t.Fatalf("outcome lost: err=%v head=%d calls=%d", err, h.LatestServerIndex, calls)
	}
}
