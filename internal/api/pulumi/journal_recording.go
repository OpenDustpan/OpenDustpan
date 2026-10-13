//go:build journalrecording

package pulumi

// maxSupportedJournalVersion is the highest journal version that the recording
// build answers. Only a build with the journalrecording tag may answer 1, and
// the Makefile refuses a release build with that tag (ADR 43, work item 9).
const maxSupportedJournalVersion = 1
