// Exercises identifier generation and rejection before a commit reaches Things Cloud.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
	memory "github.com/arthursoares/things-cloud-sdk/state/memory"
)

func TestGeneratedIdentifiersAreCanonical(t *testing.T) {
	for i := 0; i < 8192; i++ {
		id := generateUUID()
		if err := thingscloud.ValidateUUID(id); err != nil {
			t.Fatalf("generated non-canonical identifier %q: %v", id, err)
		}
	}
}

func TestInvalidCommitIdentifierRejectedBeforeSync(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "current-item-index": 0, "schema": 301})
	}))
	defer backend.Close()
	client := thingscloud.New(backend.URL, "test@example.com", "password", thingscloud.WithRequestInterval(0))
	instance := &ThingsMCP{client: client, history: &thingscloud.History{Client: client, ID: "history", LatestSchemaVersion: 301}, state: memory.NewState()}
	err := instance.writeAndSync(writeEnvelope{id: "2drXXUnFzwcLwp75NEn2", kind: "Task6", action: 0, payload: map[string]any{"tt": "Invalid ID"}})
	if err == nil || calls != 0 {
		t.Fatalf("invalid identifier reached backend: err=%v calls=%d", err, calls)
	}
}
