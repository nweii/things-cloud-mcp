// This file normalizes legacy history identifiers before storing semantic changes.
// Object keys and relationships share the deterministic identifiers used by current records.
package sync

import (
	"encoding/json"
	"fmt"

	things "github.com/arthursoares/things-cloud-sdk"
)

func normalizeLegacyItem(item things.Item) (things.Item, error) {
	var keys []string
	switch item.Kind {
	case things.ItemKindTaskPlain, things.ItemKindTask2, things.ItemKindTask3, things.ItemKindTask4:
		keys = []string{"ar", "pr", "agr", "tg", "rt", "dl"}
	case things.ItemKindTagPlain, things.ItemKindTag2, things.ItemKindTag:
		keys = []string{"pn"}
	case things.ItemKindAreaPlain, things.ItemKindArea:
		keys = []string{"tg"}
	case things.ItemKindChecklistItem, things.ItemKindChecklistItem2:
		keys = []string{"ts"}
	case things.ItemKindTombstonePlain:
		keys = []string{"dloid"}
	default:
		return item, nil
	}
	encode := func(id string) string {
		if things.ValidateUUID(id) == nil {
			return id
		}
		return things.EncodeLegacyIdentifier(id)
	}
	item.UUID = encode(item.UUID)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item.P, &fields); err != nil {
		return item, fmt.Errorf("decoding legacy references: %w", err)
	}
	for _, key := range keys {
		value, exists := fields[key]
		if !exists || string(value) == "null" {
			continue
		}
		if key == "dloid" {
			var id string
			if err := json.Unmarshal(value, &id); err != nil {
				return item, fmt.Errorf("decoding legacy deletion reference: %w", err)
			}
			fields[key], _ = json.Marshal(encode(id))
		} else {
			var ids []string
			if err := json.Unmarshal(value, &ids); err != nil {
				return item, fmt.Errorf("decoding legacy relationship %s: %w", key, err)
			}
			for i := range ids {
				ids[i] = encode(ids[i])
			}
			fields[key], _ = json.Marshal(ids)
		}
	}
	var err error
	item.P, err = json.Marshal(fields)
	return item, err
}
