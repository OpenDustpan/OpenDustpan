//go:build !journalrecording

package dustpan

import "opendustpan.org/opendustpan/pkg/stack"

// journalWriter returns no journal writer in the release build. The server
// answers journal version 0 for every update, so no batch reaches it. A batch
// that did would be refused with stack.ErrNotJournaled.
func journalWriter(*settings) (*stack.JournalEntries, error) {
	//nolint:nilnil // The release build has no journal writer, so nil is the value.
	return nil, nil
}
