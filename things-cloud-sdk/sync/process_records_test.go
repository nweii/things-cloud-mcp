// These tests exercise supported history kinds, identifier migration, and atomic rejection.
package sync

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
)

func recordSyncer(t *testing.T) *Syncer {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "records.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProcessLegacyTaskAndTagLifecycle(t *testing.T) {
	s := recordSyncer(t)
	const taskID = "AAAAAAAA-1111-2222-3333-BBBBBBBBBBBB"
	const tagID = "CCCCCCCC-1111-2222-3333-DDDDDDDDDDDD"
	const parentID = "EEEEEEEE-1111-2222-3333-FFFFFFFFFFFF"
	canonicalTask := things.EncodeLegacyIdentifier(taskID)
	canonicalTag := things.EncodeLegacyIdentifier(tagID)
	items := []things.Item{
		{UUID: tagID, Kind: things.ItemKindTag2, P: json.RawMessage(`{"tt":"Tag","pn":["` + parentID + `"]}`)},
		{UUID: taskID, Kind: things.ItemKindTask2, P: json.RawMessage(`{"tt":"Task","tg":["` + tagID + `"]}`)},
		{UUID: taskID, Kind: things.ItemKindTask2, Action: things.ItemActionModified, P: json.RawMessage(`{"tt":"Renamed"}`)},
		{UUID: canonicalTask, Kind: things.ItemKindTask7, Action: things.ItemActionModified, P: json.RawMessage(`{"ss":3}`)},
		{UUID: tagID, Kind: things.ItemKindTag2, Action: things.ItemActionModified, P: json.RawMessage(`{"tt":"Renamed tag"}`)},
	}
	changes, err := s.processItems(items, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("legacy records produced no semantic changes")
	}
	task, err := s.getTask(canonicalTask)
	if err != nil || task == nil || task.Title != "Renamed" || task.Status != things.TaskStatusCompleted || len(task.TagIDs) != 1 || task.TagIDs[0] != canonicalTag {
		t.Fatalf("task=%#v err=%v", task, err)
	}
	tag, err := s.getTag(canonicalTag)
	if err != nil || tag == nil || tag.Title != "Renamed tag" || len(tag.ParentTagIDs) != 1 || tag.ParentTagIDs[0] != things.EncodeLegacyIdentifier(parentID) {
		t.Fatalf("tag=%#v err=%v", tag, err)
	}
	if old, _ := s.getTask(taskID); old != nil {
		t.Fatal("legacy task key remains")
	}
	if _, err := s.processItems([]things.Item{
		{UUID: taskID, Kind: things.ItemKindTask2, Action: things.ItemActionDeleted, P: json.RawMessage(`{}`)},
		{UUID: "FFFFFFFF-2222-3333-4444-AAAAAAAAAAAA", Kind: things.ItemKindTombstonePlain, P: json.RawMessage(`{"dloid":"` + tagID + `","dld":1}`)},
	}, 5); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ table, id string }{{"tasks", canonicalTask}, {"tags", canonicalTag}} {
		var deleted int
		if err := s.db.QueryRow("SELECT deleted FROM "+target.table+" WHERE uuid = ?", target.id).Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		if deleted != 1 {
			t.Fatalf("%s: legacy deletion left canonical object active", target.table)
		}
	}

}

func TestProcessIgnoresMailWithoutLoggingPayload(t *testing.T) {
	s := recordSyncer(t)
	var items []things.Item
	for _, kind := range []things.ItemKind{things.ItemKindCommand, things.ItemKindCommand3} {
		for _, action := range []things.ItemAction{things.ItemActionCreated, things.ItemActionModified, things.ItemActionDeleted} {
			items = append(items, things.Item{UUID: "mail", Kind: kind, Action: action, P: json.RawMessage(`{"if":{"nt":"private body"}}`)})
		}
	}
	items = append(items, things.Item{Kind: "Settings42", Action: 99, P: json.RawMessage(`not-json`)})
	changes, err := s.processItems(items, 0)
	if err != nil || len(changes) != 0 {
		t.Fatalf("changes=%v err=%v", changes, err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM change_log").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("mail created persisted change logs")
	}
}

func TestProcessRejectsInvalidBatchBeforePersistence(t *testing.T) {
	for _, bad := range []things.Item{
		{UUID: "future", Kind: "Command4", P: json.RawMessage(`{"secret":"private body"}`)},
		{UUID: "future", Kind: "Task9", P: json.RawMessage(`{"secret":"private body"}`)},
		{UUID: "bad", Kind: things.ItemKindTask2, P: json.RawMessage(`{"tt":42}`)},
		{UUID: "bad", Kind: things.ItemKindTask2, P: json.RawMessage(`null`)},
		{UUID: "bad", Kind: things.ItemKindTombstonePlain, P: json.RawMessage(`{"dloid":42}`)},
		{UUID: "bad", Kind: things.ItemKindTask7, Action: 99, P: json.RawMessage(`{}`)},
	} {
		t.Run(string(bad.Kind)+string(bad.P), func(t *testing.T) {
			s := recordSyncer(t)
			_, err := s.processItems([]things.Item{{UUID: "known", Kind: things.ItemKindTask, P: json.RawMessage(`{"tt":"Known"}`)}, bad}, 0)
			if err == nil {
				t.Fatal("invalid batch accepted")
			}
			if strings.Contains(err.Error(), "private body") {
				t.Fatal("error exposed payload")
			}
			if task, _ := s.getTask("known"); task != nil {
				t.Fatal("valid prefix persisted")
			}
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM change_log").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("invalid batch persisted changes")
			}
			if _, err := s.processItem(bad, 0, time.Now()); err == nil {
				t.Fatal("direct dispatch accepted invalid record")
			}
		})
	}
}
