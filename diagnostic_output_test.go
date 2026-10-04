// Diagnostic reports must use JSON arrays even when an account has no warnings or errors.
package main

import (
	"encoding/json"
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

func TestDiagnosticEmptyCollectionsMatchSchema(t *testing.T) {
	encoded, err := json.Marshal(emptyDiagReport())
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"steps", "warnings", "errors"} {
		if string(report[field]) != "[]" {
			t.Errorf("%s = %s, want []", field, report[field])
		}
	}
}

func TestDiagnosticsDescribeTask7Records(t *testing.T) {
	fc := newFakeCloud("test@example.com", thingscloud.Item{UUID: thingscloud.NewUUID(), Kind: thingscloud.ItemKindTask7, Action: thingscloud.ItemActionCreated, P: json.RawMessage(`{"tt":"Current task record","cd":1791000000,"ix":1}`)})
	defer fc.Close()
	instance := newTestThingsMCP(t, fc)
	report := emptyDiagReport()
	warnings, failures := []string{}, []string{}
	instance.diagnoseSteps4to7(instance.client.HistoryWithID(fc.historyID), report, &warnings, &failures)
	if len(failures) != 0 {
		t.Fatalf("diagnosis failed: %v", failures)
	}
	for _, step := range report.Steps {
		if step.Step != 4 {
			continue
		}
		encoded, err := json.Marshal(step.Details)
		if err != nil {
			t.Fatal(err)
		}
		var details struct {
			TailItems []struct{ Kind, Title, CreationDate string } `json:"tailItems"`
		}
		if err := json.Unmarshal(encoded, &details); err != nil {
			t.Fatal(err)
		}
		if len(details.TailItems) != 1 || details.TailItems[0].Kind != "Task7" || details.TailItems[0].Title != "Current task record" || details.TailItems[0].CreationDate == "" {
			t.Fatalf("Task7 diagnostic metadata missing: %s", encoded)
		}
		return
	}
	t.Fatal("diagnostic fetch step missing")
}
