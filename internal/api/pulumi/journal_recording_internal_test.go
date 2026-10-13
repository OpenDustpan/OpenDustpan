//go:build journalrecording

package pulumi

import "testing"

// TestRecordingBuildAnswersJournalVersionOne checks that the recording build
// answers journal version 1, so the e2e suite drives the journal route.
func TestRecordingBuildAnswersJournalVersionOne(t *testing.T) {
	t.Parallel()

	if maxSupportedJournalVersion != 1 {
		t.Fatalf("recording build answers journal version %d, want 1", maxSupportedJournalVersion)
	}
}
