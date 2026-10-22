package stack

import (
	"context"
	"io"
)

// JournalReplayer replays the journal entries of one Pulumi update on a base
// checkpoint. The journal package implements it. Its conformance suite is
// in package replayertest, and a corpus of recorded CLI updates is its input.
type JournalReplayer interface {
	// Replay applies the entries of batches to the base checkpoint and writes
	// the checkpoint that results to out. Each batch is a stored batch body,
	// in the order the update sent them, and the entries apply in that order,
	// batch by batch. A replay past one of its bounds returns an error that
	// holds a ReplayBound. openBase
	// returns a new reader of the base for each pass. Replay may read the base
	// two times (ADR 43, decision 7.3), so it closes each reader it opens. The
	// output goes to out and is never held in full. Replay returns an error
	// when an entry does not apply to the checkpoint built so far, an error
	// that holds the error of openBase when the base does not open, and an
	// error that holds the context error when ctx ends.
	Replay(
		ctx context.Context,
		openBase func(context.Context) (io.ReadCloser, error),
		batches [][]byte,
		out io.Writer,
	) error
}

// ReplayBound is the refusal of a replay past one of its bounds, for example
// the number of entries of one update. A JournalReplayer returns an error
// that holds one, so the server answers 413 and names the bound. Its method
// uses standard types only, so an implementation needs no import of this
// package.
type ReplayBound interface {
	error
	ReplayBound() ReplayLimit
}

// ReplayLimit names the bound that a replay passed, the counted value and the
// limit. It is an alias of an unnamed struct type of standard types, so an
// implementation of ReplayBound declares the same type and needs no import of
// this package.
type ReplayLimit = struct {
	Bound string
	Count int64
	Limit int64
}
