package pulumijournal

import (
	"bytes"
	"fmt"
)

// Verifier checks a journal batch when it arrives. It satisfies
// stack.BatchVerifier without importing that package. Each entry must pass
// the rules of decision 5.5, which DecodeEntries applies. Each entry must also
// be one that the replayer can apply, and each reference to an earlier entry must
// name an operation that the update holds or an earlier entry of the batch. Then
// every stored entry can replay, and the stored set never refers to a missing
// entry.
//
// Only a success entry with a state creates a resource that later entries can
// name. The held set therefore holds only those operations, matching the index
// that the replay builds. A reference to a begun-only operation is refused with
// ErrMissingReference.
type Verifier struct{}

// VerifyBatch returns nil when the batch passes the checks. The errors wrap the
// sentinels of this package: ErrVersion, ErrUnknownKind, ErrKindVersion,
// ErrSequenceID, ErrUnsupportedKind and ErrMissingReference. known holds the
// completed operations that the update already holds: the operation IDs of
// success entries with a state. A reference must name one of them, or a
// completed entry earlier in the same batch.
func (Verifier) VerifyBatch(body []byte, version int, known []int64) error {
	check := newBatchCheck(known)

	// recordsChanged is set by an entry that changes the replay records. A write
	// entry (kind 5) must come before all of them (upstream backend/journal.go of
	// github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 295 to 298, expects that order).
	return DecodeEntries(bytes.NewReader(body), version, func(entry JournalEntry) error {
		if err := replayable(entry); err != nil {
			return err
		}

		if err := check.writeOrder(entry); err != nil {
			return err
		}

		check.noteRecords(entry)
		check.trackCompletion(entry)

		if err := check.references(entry); err != nil {
			return err
		}

		check.trackRebuild(entry)

		return nil
	})
}

// batchCheck holds the verifier state for one batch: the completed operations
// that references can name, and whether an entry changed the replay records.
type batchCheck struct {
	held           map[int64]bool
	recordsChanged bool
}

// newBatchCheck returns a check over the completed operations that the update
// already holds.
func newBatchCheck(known []int64) *batchCheck {
	held := make(map[int64]bool, len(known))
	for _, op := range known {
		held[op] = true
	}

	return &batchCheck{held: held, recordsChanged: false}
}

// writeOrder refuses a write entry after an entry that changes the records.
func (c *batchCheck) writeOrder(entry JournalEntry) error {
	if entry.Kind == JournalEntryKindWrite && c.recordsChanged {
		return fmt.Errorf("entry %s: %w", entryLabel(entry), ErrWriteOrder)
	}

	return nil
}

// noteRecords records whether the entry changes the replay records.
func (c *batchCheck) noteRecords(entry JournalEntry) {
	if changesRecords(entry.Kind) {
		c.recordsChanged = true
	}
}

// trackCompletion records a completed entry before its references are checked,
// as the replay adds its index before it applies its removals and flags. A
// reference to the entry itself therefore passes in both.
func (c *batchCheck) trackCompletion(entry JournalEntry) {
	if completed(entry) {
		c.held[entry.OperationID] = true
	}
}

// references refuses a reference to an operation that is not held.
func (c *batchCheck) references(entry JournalEntry) error {
	for _, ref := range references(entry) {
		if !c.held[ref] {
			return fmt.Errorf("entry %s: operation %d: %w", entryLabel(entry), ref, ErrMissingReference)
		}
	}

	return nil
}

// trackRebuild drops the held operations after a rebuilt base state entry, as
// the replay drops its records. Later references to earlier operations are
// then refused.
func (c *batchCheck) trackRebuild(entry JournalEntry) {
	if entry.Kind == JournalEntryKindRebuiltBaseState {
		clear(c.held)
	}
}

// completed reports whether an entry creates a resource that later entries can
// name. Only a success entry with a state does, matching the index that the
// replay builds from applied entries.
func completed(entry JournalEntry) bool {
	return entry.Kind == JournalEntryKindSuccess && present(entry.State)
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
