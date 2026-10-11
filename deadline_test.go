// Deadline tests cover edit wire payloads, readback, and published MCP guidance.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

func TestEditItemDeadline(t *testing.T) {
	original := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	replacement := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, kind := range []struct {
		name   string
		typeID thingscloud.TaskType
	}{{"task", thingscloud.TaskTypeTask}, {"project", thingscloud.TaskTypeProject}} {
		for _, test := range []struct {
			name    string
			args    map[string]any
			want    *time.Time
			wire    string
			present bool
		}{
			{"set", map[string]any{"deadline": "2026-11-20"}, &replacement, "1795132800", true},
			{"clear", map[string]any{"deadline": "none"}, nil, "null", true},
			{"omitted", map[string]any{"title": "Renamed"}, &original, "", false},
			{"blank", map[string]any{"deadline": ""}, &original, "", false},
		} {
			t.Run(kind.name+"/"+test.name, func(t *testing.T) {
				id := generateUUID()
				fc := newFakeCloud("deadline@example.com", makeTaskItem(id, withTitle("Deadline fixture"), withTaskType(kind.typeID), withDeadline(original)))
				defer fc.Close()
				client := newTestThingsMCP(t, fc)
				test.args["uuid"] = id
				result, err := client.handleEditTask(context.Background(), makeReq(test.args))
				if err != nil || result.IsError {
					t.Fatalf("edit failed: err=%v result=%+v", err, result)
				}
				if !strings.Contains(resultText(t, result), `"updated"`) {
					t.Fatalf("unexpected result: %s", resultText(t, result))
				}
				commits := fc.getCommitLog()
				if len(commits) != 1 {
					t.Fatalf("commits = %d, want 1", len(commits))
				}
				var body map[string]struct {
					Payload map[string]json.RawMessage `json:"p"`
				}
				if err := json.Unmarshal(commits[0], &body); err != nil {
					t.Fatal(err)
				}
				dd, present := body[id].Payload["dd"]
				if present != test.present || string(dd) != test.wire {
					t.Fatalf("wire dd = %s (present %t), want %s (present %t)", dd, present, test.wire, test.present)
				}
				shown, err := client.handleShowTask(context.Background(), makeReq(map[string]any{"uuid": id}))
				if err != nil || shown.IsError {
					t.Fatalf("readback failed: err=%v result=%+v", err, shown)
				}
				var readback struct {
					Deadline *string `json:"deadlineDate"`
				}
				if err := json.Unmarshal([]byte(resultText(t, shown)), &readback); err != nil {
					t.Fatal(err)
				}
				if test.want == nil {
					if readback.Deadline != nil {
						t.Fatalf("deadline was not cleared: %s", *readback.Deadline)
					}
				} else if readback.Deadline == nil || !strings.HasPrefix(*readback.Deadline, test.want.Format("2006-01-02")) {
					t.Fatalf("deadline readback = %v, want %s", readback.Deadline, test.want.Format("2006-01-02"))
				}
			})
		}
	}
}

func TestEditItemDeadlineGuidance(t *testing.T) {
	for _, tool := range defineTools(NewUserManager()) {
		if tool.Tool.Name != "things_edit_item" {
			continue
		}
		encoded, err := json.Marshal(tool.Tool)
		if err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(encoded, &listed); err != nil {
			t.Fatal(err)
		}
		for _, phrase := range []string{"YYYY-MM-DD", `"none" to clear`, "empty string", "leave unchanged"} {
			if !strings.Contains(listed.InputSchema.Properties["deadline"].Description, phrase) {
				t.Errorf("deadline parameter guidance missing %q", phrase)
			}
		}
		if !strings.Contains(listed.Description, "deadline=none") {
			t.Error("tool description does not expose deadline clearing")
		}
		return
	}
	t.Fatal("things_edit_item is missing")
}
