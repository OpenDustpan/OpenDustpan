//go:build !journalrecording

package pulumi

// maxSupportedJournalVersion is the highest journal version that the server
// answers. The release build answers 0 for every update, so the Pulumi CLI
// saves checkpoints (ADR 43, decision 5.4). The recording build sets 1 with the
// journalrecording build tag. Version 2 waits for work item 12.
const maxSupportedJournalVersion = 0
