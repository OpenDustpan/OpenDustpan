//go:build !journalrecording

package pulumi

// maxSupportedJournalVersion is the highest journal version that the server
// answers. The release build answers 0 for every update, so the Pulumi CLI
// saves checkpoints. The recording build sets 2 with the journalrecording
// build tag.
const maxSupportedJournalVersion = 0
