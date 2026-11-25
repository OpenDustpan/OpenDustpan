package mfasvc_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
)

// storedPasskeyInput names the passkey of storedPasskey.
type storedPasskeyInput struct {
	personID string
	id       string
	body     string
	name     string
}

// zeroPasskey returns a passkey with every field set to its zero value.
func zeroPasskey() mfa.Passkey {
	return mfa.Passkey{
		CreatedAt: time.Time{}, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "", PersonID: "", Name: "", CredentialID: nil, PublicKey: nil,
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}
}

// storedPasskey stores a passkey of the person and returns it.
func storedPasskey(t *testing.T, f *fixture, in storedPasskeyInput) mfa.Passkey {
	t.Helper()

	p := zeroPasskey()
	p.CreatedAt = f.clock.now()
	p.ID = in.id
	p.PersonID = in.personID
	p.Name = in.name
	p.CredentialID = []byte(in.body)
	p.PublicKey = []byte("cose-" + in.id)

	if err := f.store.AddPasskey(t.Context(), p, base(f.clock.now(), "passkey:"+in.id)); err != nil {
		t.Fatalf("add passkey %q: %v", in.id, err)
	}

	return p
}

// TestBreakGlassRotate proves that the service rotates a credential in one
// call, refuses an unknown passkey or one of another person, and writes the
// audit entry of each change.
func TestBreakGlassRotate(t *testing.T) {
	t.Parallel()

	t.Run("rotation by the service", func(t *testing.T) {
		t.Parallel()
		checkRotateByService(t)
	})

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()
		checkRotateUnknown(t)
	})

	t.Run("audit entry", func(t *testing.T) {
		t.Parallel()
		checkRotateAudit(t)
	})
}

// checkRotateByService rotates the only passkey and proves the stored result.
func checkRotateByService(t *testing.T) {
	t.Helper()

	f := newFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: alice, id: "pky-old", body: "old-material", name: "old-key",
	})

	next := zeroPasskey()
	next.Name = "new-key"
	next.CredentialID = []byte("credential-new")
	next.PublicKey = []byte("cose-new")
	next.Transports = []string{mfa.TransportUSB}
	next.BackupEligible = true

	if err := f.svc.RotatePasskey(t.Context(), actor(alice, "ses-alice"), alice, old.ID, next); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	keys, err := f.store.Passkeys(t.Context(), alice)
	if err != nil {
		t.Fatalf("list passkeys: %v", err)
	}

	if len(keys) != 1 || keys[0].Name != "new-key" || keys[0].PersonID != alice {
		t.Fatalf("passkeys = %+v, want the new one of %q", keys, alice)
	}

	oldKey, err := f.store.PasskeyByCredentialID(t.Context(), []byte("credential-old"))
	if !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("old passkey = %+v, %v; want ErrNotFound", oldKey, err)
	}
}

// checkRotateUnknown proves that an unknown passkey and one of another person
// leave both people unchanged.
func checkRotateUnknown(t *testing.T) {
	t.Helper()

	f := newFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: alice, id: "pky-old", body: "old-material", name: "old-key",
	})
	other := storedPasskey(t, f, storedPasskeyInput{
		personID: bob, id: "pky-bob", body: "bob-material", name: "bob-key",
	})

	for _, id := range []string{"pky-missing", other.ID} {
		next := zeroPasskey()
		next.Name = "new-key"
		next.CredentialID = []byte("credential-" + id)
		next.PublicKey = []byte("cose-new")

		err := f.svc.RotatePasskey(t.Context(), actor(alice, "ses-alice"), alice, id, next)
		if !errors.Is(err, mfa.ErrNotFound) {
			t.Fatalf("rotate %q = %v, want ErrNotFound", id, err)
		}
	}

	keys, err := f.store.Passkeys(t.Context(), alice)
	if err != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want the old one", alice, keys, err)
	}

	if keys, err = f.store.Passkeys(t.Context(), bob); err != nil || len(keys) != 1 {
		t.Fatalf("passkeys of %q = %+v, %v; want the passkey of another person", bob, keys, err)
	}
}

// checkRotateAudit proves the success entry of a rotation.
func checkRotateAudit(t *testing.T) {
	t.Helper()

	f := newFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: alice, id: "pky-old", body: "old-material", name: "old-key",
	})

	next := zeroPasskey()
	next.Name = "new-key"
	next.CredentialID = []byte("credential-new")
	next.PublicKey = []byte("cose-new")

	if err := f.svc.RotatePasskey(t.Context(), actor(alice, "ses-alice"), alice, old.ID, next); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	rotated := f.entries(t, mfasvc.ActionPasskeyRotated)
	if len(rotated) != 1 {
		t.Fatalf("rotation entries = %d, want 1", len(rotated))
	}

	keys, err := f.store.Passkeys(t.Context(), alice)
	if err != nil || len(keys) != 1 {
		t.Fatalf("passkeys = %+v, %v; want one", keys, err)
	}

	if rotated[0].Object != mfasvc.Object(keys[0].ID) || rotated[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("rotation entry = %+v, want a success for %q", rotated[0], mfasvc.Object(keys[0].ID))
	}

	checkRotateChanges(t, rotated[0], old.ID, keys[0].ID)
}

// checkRotateChanges proves the old and the new passkey of the entry.
func checkRotateChanges(t *testing.T, entry audit.Entry, oldID, newID string) {
	t.Helper()

	changes := map[string]audit.Change{}
	for _, change := range entry.Changes {
		changes[change.Key] = change
	}

	if got, ok := changes["oldPasskey"]; !ok || got.Old == nil || *got.Old != oldID {
		t.Fatalf("old passkey change = %+v, want %q", got, oldID)
	}

	if got, ok := changes["newPasskey"]; !ok || got.New != newID {
		t.Fatalf("new passkey change = %+v, want %q", got, newID)
	}
}
