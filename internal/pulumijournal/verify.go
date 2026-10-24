package pulumijournal

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Verifier checks a journal batch when it arrives. Its ReadBatch satisfies
// stack.BatchReader without importing that package. Each entry must pass
// the rules of decision 5.5, which DecodeEntries applies. Each entry must also
// be one that the replayer can apply, and each reference to an earlier entry must
// name an operation that the update holds or an earlier entry of the batch. A
// rebuilt base state entry drops the held operations, as the replay drops its
// records, and the store clears the operations of earlier batches when it
// stores such a batch. Then every stored entry can replay, and the stored set
// never refers to a missing entry.
//
// Only a success entry with a state creates a resource that later entries can
// name. The held set therefore holds only those operations, matching the index
// that the replay builds. A reference to a begun-only operation is refused with
// ErrMissingReference.
type Verifier struct{}

// VerifyBatch returns whether the batch holds an entry that changes the replay
// records, and nil when the batch passes the checks. The errors wrap the
// sentinels of this package: ErrVersion, ErrUnknownKind, ErrKindVersion,
// ErrSequenceID, ErrUnsupportedKind and ErrMissingReference. known holds the
// completed operations that the update already holds: the operation IDs of
// success entries with a state. A reference must name one of them, or a
// completed entry earlier in the same batch. A write entry (kind 5) is refused
// with ErrWriteOrder when recordsChanged is set, or when an earlier entry of the
// batch changes the records.
func (Verifier) VerifyBatch(
	body []byte,
	version int,
	known []int64,
	recordsChanged bool,
) (bool, error) {
	check := newBatchCheck(known, recordsChanged)

	err := DecodeEntries(bytes.NewReader(body), version, check.entry)
	if err != nil {
		return false, err
	}

	return check.changed, nil
}

// BatchSummary is a journal batch that ReadBatch decoded: the number of its
// entries, their sequence IDs in the order of the body, the completed
// operations after its last rebuilt base state entry, whether it holds such
// an entry, and the check of its entries against the stored state of the
// update. It is an alias of an unnamed struct type of standard types, the same
// type as stack.BatchSummary, so ReadBatch satisfies stack.BatchReader
// without importing that package.
type BatchSummary = struct {
	Entries     int
	SequenceIDs []int64
	Operations  []int64
	HasRebuild  bool
	Verify      func(known []int64, recordsChanged bool) (changed bool, err error)
}

// ReadBatch decodes body once, with the checks of DecodeEntries and the
// negotiated version, and returns its summary. Verify runs the checks of
// VerifyBatch on the entries of that one decode, so the store checks the
// batch against the stored state in its transaction without a second decode.
// ReadBatch refuses a sequence ID that appears twice with ErrSequenceID. On a
// refusal, the summary holds every sequence ID that a plain JSON read of the
// body finds, also one that is not valid or appears twice, so the record of
// the refusal names them. Verify is nil.
func (Verifier) ReadBatch(body []byte, version int) (BatchSummary, error) {
	var (
		entries []JournalEntry
		seqs    []int64
		ops     []int64
		rebuild bool
	)

	seen := make(map[int64]bool)

	err := DecodeEntries(bytes.NewReader(body), version, func(entry JournalEntry) error {
		if seen[entry.SequenceID] {
			return fmt.Errorf("entry %s: the sequence ID appears twice: %w", entryLabel(entry), ErrSequenceID)
		}

		seen[entry.SequenceID] = true
		seqs = append(seqs, entry.SequenceID)
		entries = append(entries, entry)

		switch {
		case entry.Kind == JournalEntryKindRebuiltBaseState:
			// The replay drops its records, so the earlier operations are no
			// longer names that later entries can use.
			ops, rebuild = ops[:0], true
		case completed(entry):
			ops = append(ops, entry.OperationID)
		default:
			// Any other entry names no operation that later entries can use.
		}

		return nil
	})

	summary := BatchSummary{Entries: len(entries), SequenceIDs: seqs, Operations: ops, HasRebuild: rebuild, Verify: nil}
	if err != nil {
		summary.SequenceIDs = readableSequences(body)

		return summary, err
	}

	if summary.SequenceIDs == nil {
		summary.SequenceIDs = []int64{}
	}

	if summary.Operations == nil {
		summary.Operations = []int64{}
	}

	summary.Verify = func(known []int64, recordsChanged bool) (bool, error) {
		check := newBatchCheck(known, recordsChanged)

		for i := range entries {
			if err := check.entry(entries[i]); err != nil {
				return false, err
			}
		}

		return check.changed, nil
	}

	return summary, nil
}

// batchCheck holds the verifier state for one batch: the completed operations
// that references can name, the operations an entry of this batch removed, whether
// a stored batch of the update changed the replay records, and whether an earlier
// entry of this batch did.
type batchCheck struct {
	held    map[int64]bool
	removed map[int64]bool
	stored  bool
	changed bool
}

// newBatchCheck returns a check over the completed operations that the update
// already holds, and the records flag of its stored batches.
func newBatchCheck(known []int64, stored bool) *batchCheck {
	held := make(map[int64]bool, len(known))
	for _, op := range known {
		held[op] = true
	}

	return &batchCheck{held: held, removed: make(map[int64]bool), stored: stored, changed: false}
}

// entry runs the checks of one entry, in the order of the body, and records
// what later entries need. A write entry (kind 5) must come before every entry
// that changes the replay records (upstream backend/journal.go of
// github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 295 to 298, expects that
// order).
func (c *batchCheck) entry(entry JournalEntry) error {
	if err := replayable(entry); err != nil {
		return err
	}

	if err := c.writeOrder(entry); err != nil {
		return err
	}

	c.noteRecords(entry)
	c.trackCompletion(entry)

	if err := c.references(entry); err != nil {
		return err
	}

	c.trackRemoval(entry)
	c.trackRebuild(entry)

	return nil
}

// writeOrder refuses a write entry after an entry that changes the records, in
// a stored batch or earlier in this batch.
func (c *batchCheck) writeOrder(entry JournalEntry) error {
	if entry.Kind == JournalEntryKindWrite && (c.stored || c.changed) {
		return fmt.Errorf("entry %s: %w", entryLabel(entry), ErrWriteOrder)
	}

	return nil
}

// noteRecords records whether the entry changes the replay records.
func (c *batchCheck) noteRecords(entry JournalEntry) {
	if changesRecords(entry.Kind) {
		c.changed = true
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

// references refuses a reference to an operation that is not held, and a
// new-state patch for an operation that is not held or that an earlier entry
// of the batch removed. The replay refuses both with ErrMissingReference.
func (c *batchCheck) references(entry JournalEntry) error {
	for _, ref := range references(entry) {
		if !c.held[ref] {
			return fmt.Errorf("entry %s: operation %d: %w", entryLabel(entry), ref, ErrMissingReference)
		}
	}

	for _, patch := range entry.NewStatePatches {
		if !c.held[patch.OperationID] || c.removed[patch.OperationID] {
			return fmt.Errorf(
				"entry %s: operation %d: %w",
				entryLabel(entry),
				patch.OperationID,
				ErrMissingReference,
			)
		}
	}

	return nil
}

// trackRemoval records the operation that an entry removes from the new
// resources, so a later new-state patch of the batch cannot name it. The other
// references still name it, as the replay still resolves them.
func (c *batchCheck) trackRemoval(entry JournalEntry) {
	if entry.RemoveNew != nil {
		c.removed[*entry.RemoveNew] = true
	}
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
// replay. A write entry must not come after one of them. A state migration
// replaces the base and the new resources, so it changes them.
func changesRecords(kind JournalEntryKind) bool {
	switch kind {
	case JournalEntryKindBegin, JournalEntryKindSuccess, JournalEntryKindFailure,
		JournalEntryKindRefreshSuccess, JournalEntryKindOutputs, JournalEntryKindRebuiltBaseState,
		JournalEntryKindStateMigration:
		return true
	case JournalEntryKindSecretsManager, JournalEntryKindWrite, JournalEntryKindSnippets,
		JournalEntryKindExtensionParameterize:
		return false
	}

	return false
}

// replayable refuses a write entry without a snapshot. Every other known kind
// has a replay, including the state migration (see migration.go).
func replayable(entry JournalEntry) error {
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

// readableSequences returns the sequence IDs of every entry of body that a
// plain JSON read finds, in the order of the body, with no check of their
// values. It returns nil for a body that is not a JSON object with entries.
func readableSequences(body []byte) []int64 {
	var batch sequenceBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil
	}

	seqs := make([]int64, 0, len(batch.Entries))
	for _, entry := range batch.Entries {
		seqs = append(seqs, entry.SequenceID)
	}

	return seqs
}

// sequenceBatch is the part of a batch body that readableSequences reads.
type sequenceBatch struct {
	Entries []sequenceEntry `json:"entries"`
}

// sequenceEntry is the sequence ID of one entry of a sequenceBatch.
type sequenceEntry struct {
	SequenceID int64 `json:"sequenceID"`
}
