//go:build journalrecording

package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalWriter returns the journal writer of the recording build. It checks
// each batch with the replay verifier before it stores the batch. The update
// store must also hold the journal rows.
func journalWriter(cfg settings) (*stack.JournalEntries, error) {
	rows, ok := cfg.updateStore.(stack.JournalStore)
	if !ok {
		return nil, fmt.Errorf("%w: journal rows", stack.ErrMissingDependency)
	}

	journal, err := stack.NewJournalEntries(cfg.blobStore, rows, pulumijournal.Verifier{})
	if err != nil {
		return nil, fmt.Errorf("journal writer: %w", err)
	}

	return journal, nil
}
