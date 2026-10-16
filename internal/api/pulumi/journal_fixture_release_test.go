//go:build !journalrecording

package pulumi_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// journalWriterFor returns no journal writer in the release build. The fixture
// then refuses every journal batch before any write.
func journalWriterFor(_ *testing.T, _ *memBlob, _ *memUpdateStore, _ int) *stack.JournalEntries {
	return nil
}
