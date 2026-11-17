package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// scopeID is the backup of the attempt scope tests.
const scopeID = backup.ID("bkp-scope")

// scopeKey returns the key of path in the attempt directory of scopeID.
func scopeKey(attempt, path string) string {
	return "backups/" + scopeID.String() + "/" + attempt + "/" + path
}

// claimHeld is a claim check of a holder that still has the claim.
func claimHeld(context.Context) error { return nil }

// claimGone is a claim check of a holder that lost the claim.
func claimGone(context.Context) error { return durable.ErrClaimLost }

// TestNewAttemptPrefixRefusesHostileInput sends hostile attempt names and
// backup IDs to the one constructor of the prefix.
func TestNewAttemptPrefixRefusesHostileInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		id      backup.ID
		attempt string
	}{
		{name: "empty attempt", id: scopeID, attempt: ""},
		{name: "dot-dot", id: scopeID, attempt: ".."},
		{name: "dot", id: scopeID, attempt: "."},
		{name: "slash", id: scopeID, attempt: "1/2"},
		{name: "traversal", id: scopeID, attempt: "../bkp-x"},
		{name: "backslash", id: scopeID, attempt: `1\2`},
		{name: "upper case", id: scopeID, attempt: "A1"},
		{name: "space", id: scopeID, attempt: "1 2"},
		{name: "newline", id: scopeID, attempt: "1\n2"},
		{name: "nul", id: scopeID, attempt: "1\x002"},
		{name: "too long", id: scopeID, attempt: strings.Repeat("1", backup.MaxAttemptLengthForTest+1)},
		{name: "backup id with slash", id: "bkp/../x", attempt: "1"},
		{name: "empty backup id", id: "", attempt: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := backup.NewAttemptPrefixForTest(tc.id, tc.attempt); err == nil {
				t.Fatalf("newAttemptPrefix(%q, %q) = nil, want an error", tc.id, tc.attempt)
			}
		})
	}

	longest := strings.Repeat("9", backup.MaxAttemptLengthForTest)
	if err := backup.NewAttemptPrefixForTest(scopeID, longest); err != nil {
		t.Fatalf("an attempt of the largest length = %v, want nil", err)
	}
}

// TestAttemptPrefixOwnsOnlyItsDirectory proves that the prefix of attempt 1 does
// not own a sibling directory whose name starts with the same text.
func TestAttemptPrefixOwnsOnlyItsDirectory(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"backups/bkp-scope/1/manifest.json":  true,
		"backups/bkp-scope/10/manifest.json": false,
		"backups/bkp-scope/1":                false,
		"backups/bkp-scope/live.json":        false,
		"backups/bkp-scope0/1/manifest.json": false,
	} {
		got, err := backup.AttemptOwnsForTest(scopeID, "1", blob.Key(name))
		if err != nil || got != want {
			t.Errorf("owns(%q) = %v, %v, want %v", name, got, err, want)
		}
	}
}

// TestStaleAttemptDeleteKeepsSurvivor replays the late clear of a holder that
// lost its claim after the survivor finished.
func TestStaleAttemptDeleteKeepsSurvivor(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	survivorFiles := []string{"manifest.json", "tables/a.ndjson"}

	for _, path := range survivorFiles {
		store.storeObject(t, scopeKey("2", path), []byte("survivor"))
	}

	store.storeObject(t, scopeKey("1", "tables/a.ndjson"), []byte("stale"))

	// The stale holder has no claim: its delete reaches no object at all.
	lost := backup.NewFencedServiceForTest(store, claimGone)
	if err := lost.ClearPartialForTest(t.Context(), scopeID, "1"); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("clear without a claim = %v, want ErrClaimLost", err)
	}

	if !store.has(scopeKey("1", "tables/a.ndjson")) {
		t.Fatal("a holder without a claim deleted its own file")
	}

	// A holder with a claim, late, clears only its own directory.
	held := backup.NewFencedServiceForTest(store, claimHeld)
	if err := held.ClearPartialForTest(t.Context(), scopeID, "1"); err != nil {
		t.Fatalf("clear = %v, want nil", err)
	}

	if store.has(scopeKey("1", "tables/a.ndjson")) {
		t.Error("the stale file stayed")
	}

	for _, path := range survivorFiles {
		if !store.has(scopeKey("2", path)) {
			t.Errorf("the stale delete removed survivor file %q", path)
		}
	}
}

// TestFencedServiceChecksTheClaimBeforeEachCall proves that a put and a delete
// of a holder without a claim reach no object.
func TestFencedServiceChecksTheClaimBeforeEachCall(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	kept := scopeKey("1", "manifest.json")
	other := scopeKey("1", "other.json")

	store.storeObject(t, kept, []byte("kept"))

	svc := backup.NewFencedServiceForTest(store, claimGone)

	if err := svc.ClearPartialForTest(t.Context(), scopeID, "1"); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("delete = %v, want ErrClaimLost", err)
	}

	if err := svc.PutForTest(t.Context(), mustKey(t, other), []byte("x")); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("put = %v, want ErrClaimLost", err)
	}

	if !store.has(kept) || store.has(other) {
		t.Error("a call without a claim reached the store")
	}
}
