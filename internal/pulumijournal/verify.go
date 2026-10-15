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

// VerifyBatch returns whether the batch holds an entry that changes the replay
// records, and nil when the batch passes the checks. The errors wrap the
// sentinels of this package: ErrVersion, ErrUnknownKind, ErrKindVersion,
// ErrSequenceID, ErrUnsupportedKind and ErrMissingReference. A write entry (kind 5)
// is refused with ErrWriteOrder when recordsChanged is set, or when an earlier
// entry of the batch changes the records.
//
//nolint:revive // The stored records flag is the fact the check needs, not control coupling: a write entry is refused while it is set.
func (Verifier) VerifyBatch(
	body []byte,
	version int,
	known []int64,
	recordsChanged bool,
) (bool, error) {
	held := make(map[int64]bool, len(known))
	for _, op := range known {
		held[op] = true
	}

	// changed is set by an entry that changes the replay records. A write
	// entry (kind 5) must come before all of them (upstream backend/journal.go of
	// github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 295 to 298, expects that order).
	// RecordsChanged covers the stored batches of the update, and changed covers
	// the earlier entries of this batch.
	changed := false

	err := DecodeEntries(bytes.NewReader(body), version, func(entry JournalEntry) error {
		if err := replayable(entry); err != nil {
			return err
		}

		if entry.Kind == JournalEntryKindWrite && (recordsChanged || changed) {
			return fmt.Errorf("entry %s: %w", entryLabel(entry), ErrWriteOrder)
		}

		if changesRecords(entry.Kind) {
			changed = true
		}

		for _, ref := range references(entry) {
			if !held[ref] {
				return fmt.Errorf("entry %s: operation %d: %w", entryLabel(entry), ref, ErrMissingReference)
			}
		}

		held[entry.OperationID] = true

		return nil
	})
	if err != nil {
		return false, err
	}

	return changed, nil
}

// changesRecords reports whether an entry of kind changes the records of the
// replay. A write entry must not come after one of them.
func changesRecords(kind JournalEntryKind) bool {
	switch kind {
	case JournalEntryKindBegin, JournalEntryKindSuccess, JournalEntryKindFailure,
		JournalEntryKindRefreshSuccess, JournalEntryKindOutputs, JournalEntryKindRebuiltBaseState:
		return true
	case JournalEntryKindSecretsManager, JournalEntryKindWrite, JournalEntryKindSnippets,
		JournalEntryKindExtensionParameterize, JournalEntryKindStateMigration:
		return false
	}

	return false
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
