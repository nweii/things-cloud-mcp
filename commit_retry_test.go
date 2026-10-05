// Rejected ancestor conflicts may retry after sync; other failures never replay a write.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
	memory "github.com/arthursoares/things-cloud-sdk/state/memory"
)

func TestCommitRetriesOnlyRejectedAncestorConflicts(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		status                int
		response              string
		rejections, wantPosts int
		wantSuccess           bool
	}{
		{"one conflict", 409, "OutdatedAncestor", 1, 2, true},
		{"bounded conflicts", 409, "OutdatedAncestor", 10, 4, false},
		{"other conflict", 409, "AccountIssue", 10, 1, false},
		{"server error", 500, "OutdatedAncestor", 10, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts, head := 0, 0
			remoteID, id := thingscloud.NewUUID(), thingscloud.NewUUID()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					posts++
					if ancestor := r.URL.Query().Get("ancestor-index"); ancestor != strconv.Itoa(head) {
						t.Errorf("ancestor = %s, want %d", ancestor, head)
					}
					if posts <= tc.rejections {
						head++
						w.Header().Set("Things-Response", tc.response)
						w.WriteHeader(tc.status)
						return
					}
					head++
					_ = json.NewEncoder(w).Encode(map[string]any{"server-head-index": head})
					return
				}
				items := []any{}
				if r.URL.Query().Get("start-index") != strconv.Itoa(head) {
					items = append(items, map[string]any{remoteID: map[string]any{"e": "Task7", "t": 1, "p": map[string]any{"tt": "Other device changed", "ix": 1}}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "current-item-index": head, "schema": 301})
			}))
			defer server.Close()
			client := thingscloud.New(server.URL, "test@example.com", "password", thingscloud.WithRequestInterval(0))
			instance := &ThingsMCP{client: client, history: &thingscloud.History{Client: client, ID: "history", LatestSchemaVersion: 301}, state: memory.NewState()}
			err := instance.writeAndSync(writeEnvelope{id: id, action: 0, kind: "Task6", payload: map[string]any{"tt": "Requested task", "ix": 1}})
			if (err == nil) != tc.wantSuccess || posts != tc.wantPosts {
				t.Fatalf("err=%v posts=%d, want success=%v posts=%d", err, posts, tc.wantSuccess, tc.wantPosts)
			}
			if tc.wantSuccess && (instance.state.Tasks[id] == nil || instance.history.LoadedServerIndex != head || instance.state.Tasks[remoteID] == nil) {
				t.Fatalf("retry did not sync and apply the commit: %+v", instance.history)
			}
		})
	}
}

func TestHealedCommitIsAppliedLocally(t *testing.T) {
	id := thingscloud.NewUUID()
	fc := newFakeCloud("test@example.com", thingscloud.Item{UUID: id, Kind: thingscloud.ItemKindTask, Action: thingscloud.ItemActionCreated, P: json.RawMessage(`{"tt":"Legacy item","ix":-4}`)})
	defer fc.Close()
	instance := newTestThingsMCP(t, fc)
	if err := instance.writeAndSync(writeEnvelope{id: id, action: 1, kind: "Task6", payload: map[string]any{"tr": true}}); err != nil {
		t.Fatal(err)
	}
	if task := instance.state.Tasks[id]; task.Index != 1 || !task.InTrash {
		t.Fatalf("cache differs from repaired commit: %+v", task)
	}
}
