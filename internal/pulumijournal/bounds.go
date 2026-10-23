package pulumijournal

import (
	"errors"
	"fmt"
)

// Bounds of one journaled update. The byte bounds come from the build benchmark
// BenchmarkJournalBuildChange10000 in one run, which replays 10,000 resources.
// The benchmark reports the measured value in its log. The count bounds derive
// from the checkpoint byte bound below: a create or a destroy of every smallest
// realistic state that fits it needs twice that many entries and that many
// links, so every update whose built checkpoint fits the checkpoint bound
// passes. No counted number grows with the base resources: the build streams
// the base one resource at a time, and the bounds count only the entries, the
// new states and the added links of the update.
const (
	// checkpointBoundBytes is the checkpoint byte bound the count bounds derive
	// from: 32 MiB, the same bound as the checkpoint bound of the update cycle.
	checkpointBoundBytes = 32 << 20
	// smallestStateBytes is the smallest encoded realistic resource state, in
	// bytes. Measured by BenchmarkJournalBuildSmallestState in one run: 60
	// smallest-state bytes for a state with a short well-formed URN.
	smallestStateBytes = 60
)

var (
	// MaxEntriesPerUpdate bounds the journal entries of one update.
	// Derived from checkpointBoundBytes divided by smallestStateBytes: 32 MiB
	// holds 559240 smallest states, and a create or a destroy of each one
	// needs 2 entries, so the bound is 1118481 entries.
	MaxEntriesPerUpdate = 2 * checkpointBoundBytes / smallestStateBytes //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxNewStateBytesPerUpdate bounds the new resource states of one update, in bytes.
	// Measured by BenchmarkJournalBuildChange10000 in one run: 2210000 new-state-bytes.
	// The bound matches the checkpoint bound, so new states that fit it may
	// still fail only when the built checkpoint itself is too large.
	MaxNewStateBytesPerUpdate = 32 << 20 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxLinkIndexEntriesPerUpdate bounds the added links of one update, in resources.
	// Derived from checkpointBoundBytes divided by smallestStateBytes: 32 MiB
	// holds 559240 smallest states, and each one adds 1 link, so the bound is
	// 559240 links. A link is part of its state, so links that fit the state
	// bound fit the checkpoint bound too.
	// A no-op update on a large base adds no links, so it passes whatever the base holds.
	MaxLinkIndexEntriesPerUpdate = checkpointBoundBytes / smallestStateBytes //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxNewStatePlusLinkBytesPerUpdate bounds the new-state plus added-link bytes
	// of one build, in bytes. It is not a bound on heap.
	// Measured by BenchmarkJournalBuildChange10000 in one run: peak heap 25826176 bytes.
	MaxNewStatePlusLinkBytesPerUpdate = 64 << 20 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
)

// Errors of the bounds. Each message names the bound and the limit.
var (
	// ErrTooManyEntries reports an update with more entries than MaxEntriesPerUpdate.
	ErrTooManyEntries = errors.New("pulumijournal: too many journal entries in one update")
	// ErrNewStateTooLarge reports an update with more new state bytes than MaxNewStateBytesPerUpdate.
	ErrNewStateTooLarge = errors.New("pulumijournal: new states of one update exceed the limit")
	// ErrLinkIndexTooLarge reports a build with more added links than MaxLinkIndexEntriesPerUpdate.
	ErrLinkIndexTooLarge = errors.New("pulumijournal: added links of one update exceed the limit")
	// ErrAddedBytesTooLarge reports a build with more new-state plus added-link
	// bytes than MaxNewStatePlusLinkBytesPerUpdate.
	ErrAddedBytesTooLarge = errors.New("pulumijournal: new states plus added links of one update exceed the limit")
)

// BoundError reports a per-update bound refusal: the bound, the counted value
// and its limit. errors.Is matches Cause, which is one of the bound sentinels
// above. The message names the count and the limit in one clause, so callers
// map it to their own refusal without adding a prefix.
type BoundError struct {
	// Bound names the bound, for example "journal entries".
	Bound string
	// Count is the counted value that exceeds the bound.
	Count int64
	// Limit is the bound, the same value as the matching Max bound above.
	Limit int64
	// Cause is the bound sentinel that errors.Is matches.
	Cause error
}

// Error returns the refusal in one clause.
func (e *BoundError) Error() string {
	return fmt.Sprintf("%d %s exceed the limit of %d", e.Count, e.Bound, e.Limit)
}

// Unwrap returns the bound sentinel.
func (e *BoundError) Unwrap() error {
	return e.Cause
}

// ReplayLimit names the bound, the counted value and the limit of a
// BoundError. It is the same type as stack.ReplayLimit.
type ReplayLimit = struct {
	Bound string
	Count int64
	Limit int64
}

// ReplayBound returns the bound, the counted value and the limit. It
// satisfies stack.ReplayBound without importing that package, so the server
// can name the bound in its answer.
func (e *BoundError) ReplayBound() ReplayLimit {
	return ReplayLimit{Bound: e.Bound, Count: e.Count, Limit: e.Limit}
}
