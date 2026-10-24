package errcode

// The codes of the update journal of the Pulumi Cloud-compatible API. A code
// is never removed and never changes its meaning.
const (
	// JournalBatchInvalid is a journal batch that is malformed.
	JournalBatchInvalid = "journal.batch-invalid"
	// JournalBuildTimeout is a build of the state from the journal that did not
	// end in time.
	JournalBuildTimeout = "journal.build-timeout"
	// JournalConflict is a journal batch that differs from the batch stored
	// with the same sequence number.
	JournalConflict = "journal.conflict"
	// JournalIncomplete is a journal with a batch that was not stored.
	JournalIncomplete = "journal.incomplete"
)

// journalSpecs are the codes of the update journal of the Pulumi
// Cloud-compatible API.
func journalSpecs() []Spec {
	return []Spec{
		{Code: JournalBatchInvalid, Meaning: "the journal batch is malformed", Args: nil},
		{Code: JournalBuildTimeout, Meaning: "the build of the state from the journal did not end in time", Args: nil},
		{
			Code:    JournalConflict,
			Meaning: "the journal batch differs from the batch stored with the same sequence number",
			Args:    nil,
		},
		{Code: JournalIncomplete, Meaning: "a batch of the journal was not stored: cancel the update", Args: nil},
	}
}
