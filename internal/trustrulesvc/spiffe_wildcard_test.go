package trustrulesvc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// wildcardPatterns returns the SPIFFE ID patterns with a * segment that a
// rule for the mTLS exchange must never hold.
func wildcardPatterns() []string {
	return []string{
		"spiffe://example.org/ns/acme/*",
		"spiffe://td/*",
		"spiffe://td/*/x/*",
	}
}

const wildcardBundle = "trb-0123456789abcdef0123456789abcdef"

// TestSpiffeWildcardRefused sends each wildcard pattern to Create and to
// Update, as if the handler and the store checks had failed: the service
// refuses it with ErrSpiffeWildcard and ErrInvalid, and the store sees no
// write and no audit entry.
func TestSpiffeWildcardRefused(t *testing.T) {
	t.Parallel()

	for _, pattern := range wildcardPatterns() {
		t.Run("create "+pattern, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			in := input()
			in.SpiffeIDPattern = pattern
			in.Bundle = wildcardBundle

			_, err := f.svc.Create(freshSessionContext(t), person(), emptyEntry(), "acme", in)
			mustBeWildcardRefusal(t, err, f.store)
		})

		t.Run("update "+pattern, func(t *testing.T) {
			t.Parallel()

			stored := seededRule()
			f := updateRefusalFixture(t, stored, nil)

			bundle := wildcardBundle
			in := renameInput(stored.ID, "", 1)
			in.SpiffeIDPattern = &pattern
			in.Bundle = &bundle

			_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
			mustBeWildcardRefusal(t, err, f.store)
		})
	}
}

// mustBeWildcardRefusal fails the test unless err wraps ErrSpiffeWildcard and
// ErrInvalid and the store holds no rule and no audit entry.
func mustBeWildcardRefusal(t *testing.T, err error, store *fakeStore) {
	t.Helper()

	if !errors.Is(err, trustrule.ErrSpiffeWildcard) || !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("err = %v, want ErrSpiffeWildcard and ErrInvalid", err)
	}

	if len(store.created) != 0 || len(store.entries) != 0 {
		t.Fatalf("the store holds %d rules and %d entries, want none", len(store.created), len(store.entries))
	}
}
