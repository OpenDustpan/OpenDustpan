package durabletest

import (
	"maps"
	"slices"
	"time"
)

// The rules that a broken store breaks, for the sensitivity tests.
const (
	BreakToken         = breakToken
	BreakExpiry        = breakExpiry
	BreakEndExpiry     = breakEndExpiry
	BreakTakeoverSteps = breakTakeoverSteps
	BreakClock         = breakClock
	BreakTokenStays    = breakTokenStays
	BreakLive          = breakLive
	BreakTxStart       = breakTxStart
	BreakExpiryCount   = breakExpiryCount
	BreakMaxVersions   = breakMaxVersions
	BreakTickBacklog   = breakTickBacklog
	BreakNoSchedule    = breakNoScheduleOnNoWork
)

// BrokenHarness returns a harness on a store in memory that breaks rule.
func BrokenHarness(rule string) Harness {
	return newMemDB(rule).harness()
}

// BrokenScheduleHarness returns a schedule harness on a store in memory
// that breaks rule.
func BrokenScheduleHarness(rule string) ScheduleHarness {
	return newMemDB(rule).scheduleHarness()
}

// BrokenHeartbeatHarness returns a heartbeat harness on a store in memory
// that breaks rule.
func BrokenHeartbeatHarness(rule string) HeartbeatHarness {
	return newMemDB(rule).heartbeatHarness()
}

// OldBeat records a beat of owner that is age old on the clock of the store
// in m, for the tests of the pruning rule.
func (m *Mem) OldBeat(owner string, age time.Duration) {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	m.db.beats[owner] = memBeat{at: m.db.now().Add(-age), lease: time.Minute}
}

// BeatOwners returns the owners that m holds a beat of.
func (m *Mem) BeatOwners() []string {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	return slices.Sorted(maps.Keys(m.db.beats))
}
