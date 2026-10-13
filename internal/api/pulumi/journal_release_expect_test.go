//go:build !journalrecording

package pulumi_test

// startJournalVersion is the journal version that a start answers for an offer
// of 2 in this build: the release build answers 0 (ADR 43, decision 5.4).
const startJournalVersion = 0
