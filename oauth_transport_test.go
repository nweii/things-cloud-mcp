// These tests exercise HTTP authentication before requests reach MCP handlers.
// They cover token rejection, supported credentials, and safe discovery challenges.
package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequireBearerAuthentication(t *testing.T) {
	const email = "person@example.test"
	o := &OAuthServer{jwtSecret: []byte("transport-test-signing-key"), credentials: map[string]string{email: "test-password"}}
	token := func(subject string, expires time.Time) string {
		t.Helper()
		value, err := o.createJWT(map[string]any{"sub": subject, "exp": expires.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid := token(email, time.Now().Add(time.Hour))
	basic := base64.StdEncoding.EncodeToString([]byte(email + ":test-password"))
	for _, tc := range []struct {
		name, header          string
		allowed, invalidToken bool
	}{
		{"missing", "", false, false},
		{"expired", "Bearer " + token(email, time.Now().Add(-time.Minute)), false, true},
		{"forged", "Bearer not.a.jwt", false, true},
		{"unknown subject", "Bearer " + token("unknown@example.test", time.Now().Add(time.Hour)), false, true},
		{"missing subject", "Bearer " + token("", time.Now().Add(time.Hour)), false, true},
		{"empty bearer", "Bearer ", false, true},
		{"bare scheme", "Bearer", false, true},
		{"tab separator", "Bearer\t" + valid, false, false},
		{"multiple tokens", "Bearer " + valid + " extra", false, true},
		{"comma credentials", "Bearer " + valid + ", Basic " + basic, false, true},
		{"unknown scheme", "Digest credentials", false, false},
		{"invalid basic", "Basic !!!", false, false},
		{"basic without colon", "Basic " + base64.StdEncoding.EncodeToString([]byte(email)), false, false},
		{"valid bearer", "Bearer " + valid, true, false},
		{"case insensitive bearer", "bEaReR " + valid, true, false},
		{"multiple bearer spaces", "Bearer   " + valid, true, false},
		{"basic", "Basic " + basic, true, false},
		{"case insensitive basic", "bAsIc " + basic, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			req := httptest.NewRequest(http.MethodPost, "https://example.test/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			requireBearer(o, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				ctx := NewUserManager().httpContextFunc(r.Context(), r)
				info, ok := ctx.Value(userContextKey).(*UserInfo)
				if !ok {
					t.Fatal("accepted authentication missing from handler context")
				}
				if strings.EqualFold(strings.SplitN(tc.header, " ", 2)[0], "Bearer") {
					if info.Token != valid {
						t.Fatal("transport and context disagree on bearer token")
					}
				} else if info.Email != email || info.Password != "test-password" {
					t.Fatal("transport and context disagree on Basic credentials")
				}
				w.WriteHeader(http.StatusAccepted)
			}))(rec, req)
			if reached != tc.allowed {
				t.Fatalf("handler reached = %v, want %v", reached, tc.allowed)
			}
			if tc.allowed {
				if rec.Code != http.StatusAccepted {
					t.Fatalf("status = %d", rec.Code)
				}
				return
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			challenge := rec.Header().Get("WWW-Authenticate")
			if !strings.Contains(challenge, `resource_metadata="https://example.test/.well-known/oauth-protected-resource"`) {
				t.Fatalf("missing discovery URL: %q", challenge)
			}
			if strings.Contains(challenge, `error="invalid_token"`) != tc.invalidToken {
				t.Fatalf("unexpected token error: %q", challenge)
			}
			if strings.Contains(challenge, "test-password") || strings.Contains(challenge, valid) {
				t.Fatalf("challenge contains credentials: %q", challenge)
			}
		})
	}
}

func TestRequireBearerRejectsAmbiguousAuthorizationAndUnconfiguredBearer(t *testing.T) {
	for _, headers := range [][]string{{"Bearer token", "Basic credentials"}, {"Bearer token"}} {
		req := httptest.NewRequest(http.MethodPost, "https://example.test/mcp", nil)
		for _, value := range headers {
			req.Header.Add("Authorization", value)
		}
		rec := httptest.NewRecorder()
		requireBearer(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("rejected credentials reached handler")
		}))(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	}
}

func TestBearerChallengeSanitizesQuotedValues(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBearerChallenge(rec, "https://example.test/\"\\\r\n", "bad\r\n\"\\token\x7fé")
	challenge := rec.Header().Get("WWW-Authenticate")
	if strings.ContainsAny(challenge, "\r\n\\\x7fé") || strings.Count(challenge, `"`) != 6 {
		t.Fatalf("unsafe challenge: %q", challenge)
	}
}
