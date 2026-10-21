//go:build !journalrecording

package pulumi_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// journalWriterFor returns a real journal writer, as the recording build
// does, so the replay bound tests run in both builds. The release server
// still negotiates journal version zero, so no CLI journals in production.
func journalWriterFor(t *testing.T, blobs *memBlob, rows *memUpdateStore, limit int) *stack.JournalEntries {
	t.Helper()

	journal, err := stack.NewJournalEntries(blobs, rows, pulumijournal.Verifier{}, limit, stacktest.NewRefusals())
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	return journal
}
