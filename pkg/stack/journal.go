package stack

import (
	"context"
	"io"
)

// JournalReplayer replays the journal entries of one Pulumi update on a base
// checkpoint. The journal package implements it (ADR 43, decision 3.4). The
// conformance suite for it comes with the CLI corpus (ADR 43, decision 11.3).
type JournalReplayer interface {
	// Replay applies the entries, read from entries in order, to base. It
	// returns the checkpoint bytes that result. It returns an error when an
	// entry does not apply to the checkpoint built so far.
	Replay(ctx context.Context, base []byte, entries io.Reader) ([]byte, error)
}
