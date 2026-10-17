//go:build journalrecording

package pulumi

import "testing"

// TestRecordingBuildAnswersJournalVersionTwo checks that the recording build
// answers journal version 2, so the e2e suite drives the journal route with
// state migrations.
func TestRecordingBuildAnswersJournalVersionTwo(t *testing.T) {
	t.Parallel()

	if maxSupportedJournalVersion != 2 {
		t.Fatalf("recording build answers journal version %d, want 2", maxSupportedJournalVersion)
	}
}
