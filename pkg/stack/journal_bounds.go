package stack

import (
	"errors"
	"time"
)

// Bounds of a journaled update. Each number comes from the build benchmark
// BenchmarkJournalBuildChange10000, which replays 10,000 resources. The
// benchmark reports the measured value in its log.
var (
	// MaxJournalBatchBytes bounds one journal batch body in bytes.
	// Measured by BenchmarkJournalBuildChange10000: 4185585 bytes for 10,000 resources.
	MaxJournalBatchBytes = 8 << 20 //nolint:gochecknoglobals // The bound stays tunable in tests; each test restores it.
	// MaxJournalBuildDuration bounds the build of the checkpoint at the end of a journaled update.
	// Measured by BenchmarkJournalBuildChange10000: 240187607 ns/op for 10,000 resources, measured under load.
	// Whether the CLI waits past 20 minutes is not yet checked.
	MaxJournalBuildDuration = 5 * time.Minute //nolint:gochecknoglobals // The timeout stays tunable in tests; each test restores it.
)

// ErrJournalBuildTimeout reports a journal build that ran past MaxJournalBuildDuration.
var ErrJournalBuildTimeout = errors.New("journal build timed out")
