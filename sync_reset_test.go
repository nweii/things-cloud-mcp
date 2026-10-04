// Explicit cache recovery must follow the verified history and preserve state on failure.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
	memory "github.com/arthursoares/things-cloud-sdk/state/memory"
)

func TestSyncCacheResetUsesVerifiedHistory(t *testing.T) {
	fc := newFakeCloud("test@example.com", makeTaskItem("new-task", withTitle("Restored cloud data")))
	defer fc.Close()
	client := thingscloud.New(fc.server.URL, fc.email, "password", thingscloud.WithRequestInterval(0))
	oldState := memory.NewState()
	instance := &ThingsMCP{client: client, state: oldState, history: &thingscloud.History{Client: client, ID: "old-history", LoadedServerIndex: 4408, LatestServerIndex: 4408, LatestSchemaVersion: 301}}
	if err := instance.resetSyncCache(); err != nil {
		t.Fatal(err)
	}
	if instance.state == oldState || instance.history.ID != fc.historyID || instance.history.LoadedServerIndex != 1 {
		t.Fatalf("cache did not follow verified history: %+v", instance.history)
	}
	if task := instance.state.Tasks["new-task"]; task == nil || task.Title != "Restored cloud data" {
		t.Fatalf("authoritative task missing: %#v", task)
	}
	if len(fc.getCommitLog()) != 0 {
		t.Fatal("cache reset wrote cloud data")
	}
}

func TestSyncCacheResetFailurePreservesCache(t *testing.T) {
	for _, scenario := range []string{"credentials", "empty history", "unknown kind", "unsupported schema", "transport"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/version/1/account/test@example.com" {
					if scenario == "credentials" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					key := "replacement-history"
					if scenario == "empty history" {
						key = ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "SYAccountStatusActive", "history-key": key})
					return
				}
				if scenario == "transport" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				schema := 301
				if scenario == "unsupported schema" {
					schema = 302
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"unknown": map[string]any{"e": "Task99", "t": 0, "p": map[string]any{}}}}, "current-item-index": 1, "schema": schema})
			}))
			defer server.Close()
			client := thingscloud.New(server.URL, "test@example.com", "password", thingscloud.WithRequestInterval(0))
			state := memory.NewState()
			history := &thingscloud.History{Client: client, ID: "original", LoadedServerIndex: 4408}
			syncedAt := time.Now().Add(-time.Hour)
			instance := &ThingsMCP{client: client, state: state, history: history, lastSyncAt: syncedAt}
			if err := instance.resetSyncCache(); err == nil {
				t.Fatal("expected reset failure")
			}
			if instance.state != state || instance.history != history || instance.lastSyncAt != syncedAt {
				t.Fatal("failed reset mutated live cache")
			}
		})
	}
}

func TestDiagnosticToolDeclaresExplicitCacheReset(t *testing.T) {
	for _, definition := range defineTools(NewUserManager()) {
		if definition.Tool.Name == "things_diagnose" {
			option, ok := definition.Tool.InputSchema.Properties["reset_sync_cache"]
			if !ok || option == nil {
				t.Fatal("explicit reset option is absent")
			}
			return
		}
	}
	t.Fatal("diagnostic tool missing")
}
