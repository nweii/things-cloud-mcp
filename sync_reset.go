// Explicit sync recovery rebuilds a verified account history before replacing its cache.
package main

import (
	"fmt"
	"time"
)

// resetSyncCache runs while the account's opMu is held. A failed rebuild keeps
// both the original task graph and cursor intact; it never writes cloud data.
func (t *ThingsMCP) resetSyncCache() error {
	verified, err := t.client.Verify()
	if err != nil {
		return fmt.Errorf("verify account for cache reset: %w", err)
	}
	if verified.HistoryKey == "" {
		return fmt.Errorf("cache reset: account returned no authoritative history key")
	}
	candidate := &ThingsMCP{client: t.client, history: t.client.HistoryWithID(verified.HistoryKey)}
	if err := candidate.fullRebuild(); err != nil {
		return fmt.Errorf("cache reset failed; existing cache preserved: %w", err)
	}
	t.mu.Lock()
	t.state = candidate.state
	t.history = candidate.history
	t.lastSyncAt = time.Now()
	t.mu.Unlock()
	return nil
}
