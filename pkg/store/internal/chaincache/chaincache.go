// Package chaincache keeps the audit chain state that a store handle read,
// for at most audit.ChainSwitchLag (ADR 64). Both SQL adapters use it, so a
// change of the chain reaches every instance within that time.
package chaincache

import (
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Reader reads the recorded state: the state, and false when the store
// holds no record.
type Reader func() (audit.ChainState, bool, error)

// Cache holds the chain state of one store handle. The zero value reads the
// store at its first use.
type Cache struct {
	until time.Time
	mu    sync.Mutex
	on    bool
}

// Set keeps on for audit.ChainSwitchLag, until the next read of the store.
func (c *Cache) Set(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.on = on
	c.until = time.Now().Add(audit.ChainSwitchLag)
}

// On answers whether the chain is on. It answers the kept state while it
// is younger than audit.ChainSwitchLag, and else reads the store with
// read. A store with no record computes the chain, as every installation
// did before ADR 60.
func (c *Cache) On(read Reader) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if now.Before(c.until) {
		return c.on, nil
	}

	state, found, err := read()
	if err != nil {
		return false, err
	}

	c.on = !found || state == audit.ChainStateOn
	c.until = now.Add(audit.ChainSwitchLag)

	return c.on, nil
}
