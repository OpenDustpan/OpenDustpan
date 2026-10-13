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
// arrival, and keeps nothing: the route tests read the answer, not the rows.
func (m *memUpdateStore) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	if err := row.Verify(nil); err != nil {
		return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
	}

	return false, nil
}

// JournalKeys returns no keys: the fixture keeps no journal rows.
func (m *memUpdateStore) JournalKeys(context.Context, stack.ID, stack.UpdateID) ([]blob.Key, error) {
	return nil, nil
}

// JournalSweepable reports that no journal is sweepable.
func (m *memUpdateStore) JournalSweepable(context.Context, stack.ID, stack.UpdateID) (bool, error) {
	return false, nil
}
