package pulumijournal

import "errors"

// Bounds of one journaled update. Each number comes from the build benchmark
// BenchmarkJournalBuildChange10000, which replays 10,000 resources. The
// benchmark reports the measured value in its log. No counted number grows
// with the base resources: the build streams the base one resource at a time,
// and the bounds count only the entries, the new states and the added links
// of the update.
var (
	// MaxEntriesPerUpdate bounds the journal entries of one update.
	// Measured by BenchmarkJournalBuildChange10000: 20000 entries.
	MaxEntriesPerUpdate = 60000 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxNewStateBytesPerUpdate bounds the new resource states of one update, in bytes.
	// Derived from the checkpoint bound of 32 MiB until corpus measurements exist.
	// Measured by BenchmarkJournalBuildChange10000: 2210000 new-state-bytes.
	MaxNewStateBytesPerUpdate = 32 << 20 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxLinkIndexEntriesPerUpdate bounds the added links of one update, in resources.
	// Measured by BenchmarkJournalBuildChange10000: 10000 link-index.
	// A no-op update on a large base adds no links, so it passes whatever the base holds.
	MaxLinkIndexEntriesPerUpdate = 25000 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxNewStatePlusLinkBytesPerUpdate bounds the new-state plus added-link bytes
	// of one build, in bytes. It is not a bound on heap.
	// Measured by BenchmarkJournalBuildChange10000: peak heap 24780912 bytes, measured under load.
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
