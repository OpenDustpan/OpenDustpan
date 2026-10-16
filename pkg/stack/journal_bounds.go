package stack

import (
	"errors"
	"time"
)

// Bounds of a journaled update. Each number comes from the build benchmark
// BenchmarkJournalBuildChange10000, which replays 10,000 resources. The
// benchmark reports the measured value in its log.
const (
	// MaxJournalBatchBytes bounds one journal batch body in bytes.
	// Measured by BenchmarkJournalBuildChange10000: 4,185,585 bytes for 10,000 resources.
	MaxJournalBatchBytes = 8 << 20
	// MaxJournalBuildDuration bounds the build of the checkpoint at the end of a journaled update.
	// Measured by BenchmarkJournalBuildChange10000: 192ms for 10,000 resources; the CLI waits past 20 minutes without stopping.
	MaxJournalBuildDuration = 5 * time.Minute
)

// ErrJournalBuildTimeout reports a journal build that ran past MaxJournalBuildDuration.
var ErrJournalBuildTimeout = errors.New("journal build timed out")
