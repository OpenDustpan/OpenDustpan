package pulumijournal_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack/batchreadertest"
)

// TestBatchReaderConformance runs the conformance suite of stack.BatchReader
// on the verifier.
func TestBatchReaderConformance(t *testing.T) {
	t.Parallel()

	batchreadertest.Run(t, pulumijournal.Verifier{})
}
