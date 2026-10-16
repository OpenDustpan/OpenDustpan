package pulumijournal

import "errors"

// Bounds of one journaled update. Each number comes from the build benchmark
// BenchmarkJournalBuildChange10000, which replays 10,000 resources. The
// benchmark reports the measured value in its log. No number grows with the
// base resources: the build streams the base one resource at a time and keeps
// only the new states and the link index.
const (
	// MaxEntriesPerUpdate bounds the journal entries of one update.
	// Measured by BenchmarkJournalBuildChange10000: 20,000 entries.
	MaxEntriesPerUpdate = 60000
	// MaxNewStateBytesPerUpdate bounds the new resource states of one update, in bytes.
	// Measured by BenchmarkJournalBuildChange10000: 2,210,000 bytes.
	MaxNewStateBytesPerUpdate = 12 << 20
	// MaxLinkIndexEntriesPerUpdate bounds the link index of one build, in resources.
	// Measured by BenchmarkJournalBuildChange10000: 10,000 resources.
	MaxLinkIndexEntriesPerUpdate = 25000
	// MaxBuildMemoryBytes bounds the estimated memory of one build, in bytes.
	// Measured by BenchmarkJournalBuildChange10000: peak heap 34,564,080 bytes.
	MaxBuildMemoryBytes = 64 << 20
)

// Errors of the bounds. Each message names the bound and the limit.
var (
	// ErrTooManyEntries reports an update with more entries than MaxEntriesPerUpdate.
	ErrTooManyEntries = errors.New("pulumijournal: too many journal entries in one update")
	// ErrNewStateTooLarge reports an update with more new state bytes than MaxNewStateBytesPerUpdate.
	ErrNewStateTooLarge = errors.New("pulumijournal: new states of one update exceed the limit")
	// ErrLinkIndexTooLarge reports a build with more indexed resources than MaxLinkIndexEntriesPerUpdate.
	ErrLinkIndexTooLarge = errors.New("pulumijournal: link index of one build exceeds the limit")
	// ErrBuildMemoryExceeded reports a build with more estimated memory than MaxBuildMemoryBytes.
	ErrBuildMemoryExceeded = errors.New("pulumijournal: build memory exceeds the limit")
)
