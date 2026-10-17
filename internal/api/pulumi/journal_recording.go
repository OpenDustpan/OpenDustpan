//go:build journalrecording

package pulumi

// maxSupportedJournalVersion is the highest journal version that the recording
// build answers. Only a build with the journalrecording tag may answer above 0,
// and the Makefile refuses a release build with that tag.
const maxSupportedJournalVersion = 2
