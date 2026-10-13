//go:build !journalrecording

package pulumi

import "testing"

// TestReleaseBuildAnswersJournalVersionZero checks that the release build
// answers journal version 0 for every update, so no Pulumi CLI journals in
// production (ADR 43, decision 5.4 and work item 9).
func TestReleaseBuildAnswersJournalVersionZero(t *testing.T) {
	t.Parallel()

	if maxSupportedJournalVersion != 0 {
		t.Fatalf("release build answers journal version %d, want 0", maxSupportedJournalVersion)
	}
}
