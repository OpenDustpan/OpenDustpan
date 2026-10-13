package pulumijournal

import (
	"bytes"
	"fmt"
)

// Verifier checks a journal batch when it arrives (ADR 43, decision 6.4). It
// satisfies stack.BatchVerifier without importing that package. Each entry must pass
// the rules of decision 5.5, which DecodeEntries applies. Each entry must also
// be one that the replayer can apply, and each reference to an earlier entry must
// name an operation that the update holds or an earlier entry of the batch. Then
// every stored entry can replay, and the stored set never refers to a missing
// entry.
//
// A reference to the operation of a Begin entry passes this check, because the
// stored operation IDs do not say which kind introduced them. The replay refuses
// such a reference with ErrMissingReference.
type Verifier struct{}

// VerifyBatch returns nil when the batch passes the checks. The errors wrap the
// sentinels of this package: ErrVersion, ErrUnknownKind, ErrKindVersion,
// ErrSequenceID, ErrUnsupportedKind and ErrMissingReference.
func (Verifier) VerifyBatch(body []byte, version int, known []int64) error {
	held := make(map[int64]bool, len(known))
	for _, op := range known {
		held[op] = true
	}

	return DecodeEntries(bytes.NewReader(body), version, func(entry JournalEntry) error {
		if err := replayable(entry); err != nil {
			return err
		}

		for _, ref := range references(entry) {
			if !held[ref] {
				return fmt.Errorf("entry %s: operation %d: %w", entryLabel(entry), ref, ErrMissingReference)
			}
		}

		held[entry.OperationID] = true

		return nil
	})
}

// replayable refuses a kind that the replayer does not apply yet, and a write
// entry without a snapshot. Kind 10 waits for version 2 (item 12). Kinds 5 and 7
// are replayed (issue #430).
func replayable(entry JournalEntry) error {
	if entry.Kind == JournalEntryKindStateMigration {
		return fmt.Errorf("entry %s: kind %d: %w", entryLabel(entry), entry.Kind, ErrUnsupportedKind)
	}

	if entry.Kind == JournalEntryKindWrite && len(entry.NewSnapshot) == 0 {
		return fmt.Errorf("entry %s: kind %d: %w", entryLabel(entry), entry.Kind, ErrNoSnapshot)
	}

	return nil
}

// references returns the operations that an entry names as new resources: removeNew,
// deleteNew and pendingReplacementNew (ADR 43, decision 6.4).
func references(entry JournalEntry) []int64 {
	var refs []int64
	for _, ref := range []*int64{entry.RemoveNew, entry.DeleteNew, entry.PendingReplacementNew} {
		if ref != nil {
			refs = append(refs, *ref)
		}
	}

	return refs
}
