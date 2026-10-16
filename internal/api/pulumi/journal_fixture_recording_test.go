//go:build journalrecording

package pulumi_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalWriterFor returns the journal writer of the recording build, over the
// fixture's blobs and update store, with the replay verifier.
func journalWriterFor(t *testing.T, blobs *memBlob, rows *memUpdateStore, limit int) *stack.JournalEntries {
	t.Helper()

	journal, err := stack.NewJournalEntries(blobs, rows, pulumijournal.Verifier{}, limit)
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	return journal
}

// PutJournalRow checks the row with the replay verifier, as the stores do on
// arrival, and keeps its sequence IDs: the route tests read the answer and
// the complete check reads the stored sequences, not the rows.
func (m *memUpdateStore) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	if _, err := row.Verify(nil, false); err != nil {
		return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.journal[row.Update] = append(m.journal[row.Update], row.BlobKey)
	m.seqs[row.Update] = append(m.seqs[row.Update], row.SequenceIDs...)

	return false, nil
}

// JournalKeys returns the blob keys of the stored rows of the update, in
// arrival order.
func (m *memUpdateStore) JournalKeys(
	_ context.Context, _ stack.ID, update stack.UpdateID,
) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]blob.Key(nil), m.journal[update]...), nil
}

// JournalSweepable reports that no journal is sweepable.
func (m *memUpdateStore) JournalSweepable(context.Context, stack.ID, stack.UpdateID) (bool, error) {
	return false, nil
}

// RecordJournalRefusal keeps the refusal, as the stores do, so the complete
// route finds it.
func (m *memUpdateStore) RecordJournalRefusal(
	_ context.Context, refusal stack.JournalRefusal, _ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.refusals == nil {
		m.refusals = map[stack.UpdateID][]stack.JournalRefusal{}
	}

	m.refusals[refusal.Update] = append(m.refusals[refusal.Update], refusal)

	return nil
}

// JournalRefusals returns the refusals the fixture kept.
func (m *memUpdateStore) JournalRefusals(
	_ context.Context, _ stack.ID, update stack.UpdateID,
) ([]stack.JournalRefusal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.refusals[update], nil
}

// JournalSequences returns the sequence IDs the fixture kept, in order.
func (m *memUpdateStore) JournalSequences(
	_ context.Context, _ stack.ID, update stack.UpdateID,
) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := slices.Clone(m.seqs[update])
	slices.Sort(out)

	return out, nil
}
