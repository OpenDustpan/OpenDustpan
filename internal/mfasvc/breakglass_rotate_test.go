package mfasvc_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// glass is the break-glass account of the rotation tests.
const glass = "prn-glass"

// notBreakGlassCause is the cause of the refusal of a person that is not a
// break-glass account.
const notBreakGlassCause = "the person is not a break-glass account"

// newBreakGlassFixture returns a fixture with the break-glass account glass.
func newBreakGlassFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	now := f.clock.now()
	p := principal.Principal{
		CreatedAt: now, UpdatedAt: now, DisabledAt: time.Time{}, ID: glass, Subject: glass,
		DisplayName: glass, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: true,
	}

	if err := f.store.CreatePrincipal(t.Context(), p, base(now, principal.Ref(glass))); err != nil {
		t.Fatalf("create principal %s: %v", glass, err)
	}

	return f
}

// newPasskey returns a new passkey for a rotation, with the credential ID
// credential-<suffix>.
func newPasskey(suffix string) mfa.Passkey {
	next := zeroPasskey()
	next.Name = "new-key"
	next.CredentialID = []byte("credential-" + suffix)
	next.PublicKey = []byte("cose-new")

	return next
}

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

	t.Run("person that is not a break-glass account", func(t *testing.T) {
		t.Parallel()
		checkRotateNotBreakGlass(t)
	})

	t.Run("ID not valid", func(t *testing.T) {
		t.Parallel()
		checkRotateInvalidID(t)
	})
}

// checkRotateNotBreakGlass proves that the service refuses the rotation of a
// passkey of a person that is not a break-glass account, before the store,
// and that the old passkey stays.
func checkRotateNotBreakGlass(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: alice, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), alice, old.ID, newPasskey("new"))
	f.wantRefusal(t, "rotate a passkey of a person that is not a break-glass account", err, mfa.ErrNotFound,
		mfasvc.ActionRotateRefused)

	refusals := f.entries(t, mfasvc.ActionRotateRefused)
	if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != notBreakGlassCause {
		t.Fatalf("refusal changes = %+v, want the cause %q from the service", cause, notBreakGlassCause)
	}

	keys, err := f.store.Passkeys(t.Context(), alice)
	if err != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want the old one", alice, keys, err)
	}

	if got := f.entries(t, mfasvc.ActionPasskeyRotated); len(got) != 0 {
		t.Fatalf("rotation entries = %d, want 0", len(got))
	}
}

// checkRotateInvalidID proves that the service refuses a person ID or a
// passkey ID that is not valid, and that the old passkey stays.
func checkRotateInvalidID(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	cases := []struct {
		name     string
		personID string
		oldID    string
	}{
		{name: "empty person", personID: "", oldID: old.ID},
		{name: "person with a space", personID: "prn glass", oldID: old.ID},
		{name: "person with a line feed", personID: glass + "\n", oldID: old.ID},
		{name: "person too long", personID: "prn-" + strings.Repeat("a", mfa.MaxIDLength), oldID: old.ID},
		{name: "empty passkey", personID: glass, oldID: ""},
		{name: "passkey with a slash", personID: glass, oldID: "pky/old"},
		{name: "passkey with a carriage return", personID: glass, oldID: old.ID + "\r"},
		{name: "passkey too long", personID: glass, oldID: "pky-" + strings.Repeat("a", mfa.MaxIDLength)},
	}

	for i, tc := range cases {
		err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), tc.personID, tc.oldID, newPasskey("new"))
		f.wantRefusal(t, "rotate with "+tc.name, err, mfa.ErrNotFound, mfasvc.ActionRotateRefused)

		// One entry for each case so far: the newest one must be this case.
		refusals := f.entries(t, mfasvc.ActionRotateRefused)
		if len(refusals) != i+1 {
			t.Fatalf("rotate with %s: refusal entries = %d, want %d", tc.name, len(refusals), i+1)
		}

		newest := refusals[len(refusals)-1]
		if cause := newest.Changes; len(cause) != 1 || cause[0].New != "ID not valid" {
			t.Fatalf("rotate with %s: refusal changes = %+v, want the cause of the ID check", tc.name, cause)
		}
	}

	keys, err := f.store.Passkeys(t.Context(), glass)
	if err != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want the old one", glass, keys, err)
	}
}

// checkRotateByService rotates the only passkey and proves the stored result.
func checkRotateByService(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	next := zeroPasskey()
	next.Name = "new-key"
	next.CredentialID = []byte("credential-new")
	next.PublicKey = []byte("cose-new")
	next.Transports = []string{mfa.TransportUSB}
	next.BackupEligible = true

	if err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), glass, old.ID, next); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	keys, err := f.store.Passkeys(t.Context(), glass)
	if err != nil {
		t.Fatalf("list passkeys: %v", err)
	}

	if len(keys) != 1 || keys[0].Name != "new-key" || keys[0].PersonID != glass {
		t.Fatalf("passkeys = %+v, want the new one of %q", keys, glass)
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

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})
	other := storedPasskey(t, f, storedPasskeyInput{
		personID: bob, id: "pky-bob", body: "bob-material", name: "bob-key",
	})

	for _, id := range []string{"pky-missing", other.ID} {
		err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), glass, id, newPasskey(id))
		if !errors.Is(err, mfa.ErrNotFound) {
			t.Fatalf("rotate %q = %v, want ErrNotFound", id, err)
		}
	}

	keys, err := f.store.Passkeys(t.Context(), glass)
	if err != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want the old one", glass, keys, err)
	}

	if keys, err = f.store.Passkeys(t.Context(), bob); err != nil || len(keys) != 1 {
		t.Fatalf("passkeys of %q = %+v, %v; want the passkey of another person", bob, keys, err)
	}
}

// checkRotateAudit proves the success entry of a rotation.
func checkRotateAudit(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), glass, old.ID, newPasskey("new"))
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	rotated := f.entries(t, mfasvc.ActionPasskeyRotated)
	if len(rotated) != 1 {
		t.Fatalf("rotation entries = %d, want 1", len(rotated))
	}

	keys, err := f.store.Passkeys(t.Context(), glass)
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
