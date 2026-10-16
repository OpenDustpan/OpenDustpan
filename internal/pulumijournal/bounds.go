package pulumijournal

import "errors"

// Bounds of one journaled update. Each number comes from the build benchmark
// BenchmarkJournalBuildChange10000 in one run, which replays 10,000 resources.
// The benchmark reports the measured value in its log. The count bounds are
// multiples of the measured values, so every update whose built checkpoint
// fits the 32 MiB checkpoint bound passes at the measured per-resource cost:
// 10,000 changed resources need 20000 entries and 10000 added links for a
// checkpoint of 2220181 bytes, so 32 MiB holds about 150,000 such resources.
// No counted number grows with the base resources: the build streams the base
// one resource at a time, and the bounds count only the entries, the new
// states and the added links of the update.
var (
	// MaxEntriesPerUpdate bounds the journal entries of one update.
	// Measured by BenchmarkJournalBuildChange10000 in one run: 20000 entries.
	// The bound is 30 times that value, which allows four entries for each
	// of 150,000 resources: creates, refreshes and destroys of every base
	// and result that fits the checkpoint bound pass.
	MaxEntriesPerUpdate = 600000 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxNewStateBytesPerUpdate bounds the new resource states of one update, in bytes.
	// Measured by BenchmarkJournalBuildChange10000 in one run: 2210000 new-state-bytes.
	// The bound matches the checkpoint bound, so new states that fit it may
	// still fail only when the built checkpoint itself is too large.
	MaxNewStateBytesPerUpdate = 32 << 20 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxLinkIndexEntriesPerUpdate bounds the added links of one update, in resources.
	// Measured by BenchmarkJournalBuildChange10000 in one run: 10000 link-index.
	// The bound is 20 times that value, which allows the links of 150,000
	// resources with room for smaller ones.
	// A no-op update on a large base adds no links, so it passes whatever the base holds.
	MaxLinkIndexEntriesPerUpdate = 200000 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
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
