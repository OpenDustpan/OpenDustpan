//go:build journalrecording

package pulumi_test

import (
	"context"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalWriterFor returns the journal writer of the recording build, over the
// fixture's blobs and update store, with the replay verifier.
func journalWriterFor(t *testing.T, blobs *memBlob, rows *memUpdateStore) *stack.JournalEntries {
	t.Helper()

	journal, err := stack.NewJournalEntries(blobs, rows, pulumijournal.Verifier{})
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	return journal
}

// PutJournalRow checks the row with the replay verifier, as the stores do on
// arrival, and keeps its blob key, so a later build replays the stored rows.
func (m *memUpdateStore) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	if _, err := row.Verify(nil, false); err != nil {
		return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.journal[row.Update] = append(m.journal[row.Update], row.BlobKey)

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
