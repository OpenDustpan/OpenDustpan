package mfasvc_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
)

// invalidPasskeyCase is a new passkey that Passkey.Validate refuses, after the
// service sets the person and the time.
type invalidPasskeyCase struct {
	name   string
	mutate func(p *mfa.Passkey)
}

// invalidPasskeys returns the table of the invalid shapes of a new passkey.
func invalidPasskeys() []invalidPasskeyCase {
	return []invalidPasskeyCase{
		{name: "empty credential ID", mutate: func(p *mfa.Passkey) { p.CredentialID = nil }},
		{name: "credential ID too long", mutate: func(p *mfa.Passkey) {
			p.CredentialID = bytes.Repeat([]byte{1}, mfa.MaxCredentialIDBytes+1)
		}},
		{name: "empty public key", mutate: func(p *mfa.Passkey) { p.PublicKey = nil }},
		{name: "public key too long", mutate: func(p *mfa.Passkey) {
			p.PublicKey = bytes.Repeat([]byte{1}, mfa.MaxPublicKeyBytes+1)
		}},
		{name: "ID without the prefix", mutate: func(p *mfa.Passkey) { p.ID = "key-1" }},
		{name: "ID with a space", mutate: func(p *mfa.Passkey) { p.ID = "pky-a b" }},
		{name: "ID with a line feed", mutate: func(p *mfa.Passkey) { p.ID = "pky-a\n" }},
		{name: "ID too long", mutate: func(p *mfa.Passkey) { p.ID = "pky-" + strings.Repeat("a", mfa.MaxIDLength) }},
		{name: "empty name", mutate: func(p *mfa.Passkey) { p.Name = "" }},
		{name: "name with a space", mutate: func(p *mfa.Passkey) { p.Name = "new key" }},
		{name: "name with a carriage return", mutate: func(p *mfa.Passkey) { p.Name = "key\r" }},
		{name: "name that starts with a dash", mutate: func(p *mfa.Passkey) { p.Name = "-key" }},
		{name: "name too long", mutate: func(p *mfa.Passkey) {
			p.Name = strings.Repeat("a", mfa.MaxPasskeyNameLength+1)
		}},
		{name: "transport not on the allowlist", mutate: func(p *mfa.Passkey) {
			p.Transports = []string{"carrier-pigeon"}
		}},
		{name: "transport named twice", mutate: func(p *mfa.Passkey) {
			p.Transports = []string{mfa.TransportUSB, mfa.TransportUSB}
		}},
	}
}

// hostileIDs is the table of person IDs that no principal can have.
func hostileIDs() []struct{ name, id string } {
	return []struct{ name, id string }{
		{name: "empty", id: ""},
		{name: "space", id: "prn glass"},
		{name: "line feed", id: glass + "\n"},
		{name: "carriage return", id: glass + "\r"},
		{name: "slash", id: "prn/glass"},
		{name: "too long", id: "prn-" + strings.Repeat("a", mfa.MaxIDLength)},
	}
}

// wantGlassUnchanged proves that the store holds only the passkey old for
// glass and that no entry of a success exists for the actions.
func wantGlassUnchanged(t *testing.T, f *fixture, old mfa.Passkey, actions ...string) {
	t.Helper()

	keys, err := f.store.Passkeys(t.Context(), glass)
	if err != nil || len(keys) != 1 || keys[0].ID != old.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want only the old one", glass, keys, err)
	}

	for _, action := range actions {
		if got := f.entries(t, action); len(got) != 0 {
			t.Fatalf("entries of %s = %d, want 0", action, len(got))
		}
	}
}

// TestBreakGlassRotateGuards proves that RotatePasskey refuses a new passkey
// that is not valid, with each other check removed from the path: the old
// passkey stays, the store takes no write, and no success entry exists.
func TestBreakGlassRotateGuards(t *testing.T) {
	t.Parallel()

	t.Run("new passkey not valid", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		old := storedPasskey(t, f, storedPasskeyInput{
			personID: glass, id: "pky-old", body: "old-material", name: "old-key",
		})

		for _, tc := range invalidPasskeys() {
			next := newPasskey("new")
			tc.mutate(&next)

			err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), glass, old.ID, next)
			f.wantRefusal(t, "rotate with "+tc.name, err, mfa.ErrInvalid, mfasvc.ActionRotateRefused)

			refusals := f.entries(t, mfasvc.ActionRotateRefused)
			if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "the new passkey is not valid" {
				t.Fatalf("rotate with %s: refusal changes = %+v, want the cause of the passkey check", tc.name, cause)
			}

			wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
		}
	})

	t.Run("person of the new passkey is the named person", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		old := storedPasskey(t, f, storedPasskeyInput{
			personID: glass, id: "pky-old", body: "old-material", name: "old-key",
		})

		next := newPasskey("new")
		next.PersonID = bob

		if err := f.svc.RotatePasskey(t.Context(), actor(glass, "ses-glass"), glass, old.ID, next); err != nil {
			t.Fatalf("rotate: %v", err)
		}

		if keys, err := f.store.Passkeys(t.Context(), bob); err != nil || len(keys) != 0 {
			t.Fatalf("passkeys of %q = %+v, %v; want none", bob, keys, err)
		}

		keys, err := f.store.Passkeys(t.Context(), glass)
		if err != nil || len(keys) != 1 || keys[0].PersonID != glass {
			t.Fatalf("passkeys of %q = %+v, %v; want the new one of %q", glass, keys, err, glass)
		}
	})

	t.Run("no caller", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		old := storedPasskey(t, f, storedPasskeyInput{
			personID: glass, id: "pky-old", body: "old-material", name: "old-key",
		})

		err := f.svc.RotatePasskey(t.Context(), actor("", ""), glass, old.ID, newPasskey("new"))
		f.wantRefusal(t, "rotate without a caller", err, mfa.ErrWrongPerson, mfasvc.ActionRotateRefused)
		wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
	})
}

// TestBreakGlassAddRefusesOtherPerson proves that AddPasskey refuses a person
// that is not a break-glass account, and that the store takes no write.
func TestBreakGlassAddRefusesOtherPerson(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	other := storedPasskey(t, f, storedPasskeyInput{
		personID: alice, id: "pky-alice", body: "alice-material", name: "alice-key",
	})

	for _, id := range []string{alice, "prn-unknown"} {
		_, err := f.svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), id, newPasskey("new"))
		f.wantRefusal(t, "add to "+id, err, mfa.ErrNotFound, mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if len(refusals) == 0 {
			t.Fatalf("add to %s: no refusal entry", id)
		}

		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != notBreakGlassCause {
			t.Fatalf("add to %s: refusal changes = %+v, want the cause %q", id, cause, notBreakGlassCause)
		}
	}

	keys, err := f.store.Passkeys(t.Context(), alice)
	if err != nil || len(keys) != 1 || keys[0].ID != other.ID {
		t.Fatalf("passkeys of %q = %+v, %v; want only the old one", alice, keys, err)
	}

	if got := f.entries(t, mfasvc.ActionPasskeyAdded); len(got) != 0 {
		t.Fatalf("add entries = %d, want 0", len(got))
	}
}

// TestBreakGlassAddRefusesHostileID proves that AddPasskey refuses an ID that
// is not valid, and that the store takes no write.
func TestBreakGlassAddRefusesHostileID(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	for _, tc := range hostileIDs() {
		_, err := f.svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), tc.id, newPasskey("new"))
		f.wantRefusal(t, "add with person "+tc.name, err, mfa.ErrNotFound, mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "ID not valid" {
			t.Fatalf("add with person %s: refusal changes = %+v, want the cause of the ID check", tc.name, cause)
		}
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyAdded)
}

// TestBreakGlassAddRefusesInvalidPasskey proves that AddPasskey refuses a
// passkey that is not valid, and that the store takes no write.
func TestBreakGlassAddRefusesInvalidPasskey(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	for _, tc := range invalidPasskeys() {
		next := newPasskey("new")
		tc.mutate(&next)

		_, err := f.svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), glass, next)
		f.wantRefusal(t, "add with "+tc.name, err, mfa.ErrInvalid, mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "the passkey is not valid" {
			t.Fatalf("add with %s: refusal changes = %+v, want the cause of the passkey check", tc.name, cause)
		}

		wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyAdded)
	}
}

// TestBreakGlassAddStoresForNamedPerson proves that AddPasskey stores the
// passkey for the named person, whatever person the new passkey names.
func TestBreakGlassAddStoresForNamedPerson(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	next := newPasskey("new")
	next.PersonID = bob

	added, err := f.svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), glass, next)
	if err != nil || added.PersonID != glass {
		t.Fatalf("add = %+v, %v; want a passkey of %q", added, err, glass)
	}

	if keys, err := f.store.Passkeys(t.Context(), bob); err != nil || len(keys) != 0 {
		t.Fatalf("passkeys of %q = %+v, %v; want none", bob, keys, err)
	}
}

// passkeyOnlyStore exposes only the passkey port of its store. A service over
// it has no PrincipalReader, so checkBreakGlassPerson cannot read the person.
type passkeyOnlyStore struct {
	mfa.PasskeyStore
}

// errPrincipalReadFailed is the read error of failingPrincipalStore. It stands
// for a store failure, which is not the ErrNotFound of an unknown record.
var errPrincipalReadFailed = errors.New("read failed")

// failingPrincipalStore reads principals and answers errPrincipalReadFailed
// for every one.
type failingPrincipalStore struct {
	mfa.PasskeyStore

	err error
}

func (s failingPrincipalStore) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: id,
		Subject: "", DisplayName: "", Kind: principal.KindPerson, Source: "", Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}, s.err
}

// serviceOverStore returns a service over store that writes its entries to the
// store of f, so a refusal of the service is readable by the fixture.
func serviceOverStore(t *testing.T, f *fixture, store mfa.PasskeyStore) *mfasvc.Service {
	t.Helper()

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: store, RP: rp, Audit: f.store, Now: f.clock.now, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestBreakGlassFailClosed proves the two branches of checkBreakGlassPerson
// that refuse without a usable read: a store that is not a PrincipalReader,
// and a read that fails with an error other than ErrNotFound. Neither writes
// a passkey or a success entry.
func TestBreakGlassFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("store cannot read the principal", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		old := storedPasskey(t, f, storedPasskeyInput{
			personID: glass, id: "pky-old", body: "old-material", name: "old-key",
		})
		svc := serviceOverStore(t, f, passkeyOnlyStore{PasskeyStore: f.store})

		_, err := svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), glass, newPasskey("new"))
		f.wantRefusal(t, "add with a store that cannot read principals", err, mfa.ErrUnavailable,
			mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "the store cannot read the principal" {
			t.Fatalf("refusal changes = %+v, want the cause of the reader check", cause)
		}

		wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyAdded)
	})

	t.Run("principal read fails", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		old := storedPasskey(t, f, storedPasskeyInput{
			personID: glass, id: "pky-old", body: "old-material", name: "old-key",
		})
		store := failingPrincipalStore{PasskeyStore: f.store, err: errPrincipalReadFailed}
		svc := serviceOverStore(t, f, store)

		_, err := svc.AddPasskey(t.Context(), actor(glass, "ses-glass"), glass, newPasskey("new"))
		f.wantRefusal(t, "add with a failing principal read", err, mfa.ErrUnavailable, mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "the principal cannot be read" {
			t.Fatalf("refusal changes = %+v, want the cause of the read check", cause)
		}

		wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyAdded)
	})
}

// TestBreakGlassAddNeedsCaller proves that AddPasskey refuses a caller with no
// principal ID before it reads the person or writes a passkey.
func TestBreakGlassAddNeedsCaller(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	_, err := f.svc.AddPasskey(t.Context(), actor("", ""), glass, newPasskey("new"))
	f.wantRefusal(t, "add with no caller", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)

	refusals := f.entries(t, mfasvc.ActionAddRefused)
	if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "no caller" {
		t.Fatalf("refusal changes = %+v, want the cause of the caller check", cause)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyAdded)
}

// TestBreakGlassAddPersonRefusesKind proves that AddBreakGlassPerson refuses
// an input of another kind before it touches the store: one denial entry,
// and no person, no passkey and no success entry.
func TestBreakGlassAddPersonRefusesKind(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)

	q := principal.Query{After: "", Low: "", High: "", Limit: 100, InstallationOnly: false}

	persons, _, err := f.store.ListPrincipals(t.Context(), q, principal.KindPerson)
	if err != nil {
		t.Fatalf("list the persons: %v", err)
	}

	for _, kind := range []principal.Kind{principal.KindService, principal.KindWorkload} {
		in := principal.Input{Subject: "break-glass-two", DisplayName: "Break glass two", Kind: kind}

		_, _, addErr := f.svc.AddBreakGlassPerson(t.Context(), actor(glass, "ses-glass"), in, newPasskey("new"))
		f.wantRefusal(t, "add of a "+string(kind), addErr, principal.ErrBreakGlassKind, mfasvc.ActionAddRefused)

		refusals := f.entries(t, mfasvc.ActionAddRefused)
		if cause := refusals[0].Changes; len(cause) != 1 || cause[0].New != "a break-glass account is a person" {
			t.Fatalf("add of a %s: refusal changes = %+v, want the cause of the kind check", kind, cause)
		}
	}

	_, keyErr := f.store.PasskeyByCredentialID(t.Context(), []byte("credential-new"))
	if !errors.Is(keyErr, mfa.ErrNotFound) {
		t.Fatalf("passkey after the refused add: err = %v, want ErrNotFound", keyErr)
	}

	after, _, listErr := f.store.ListPrincipals(t.Context(), q, principal.KindPerson)
	if listErr != nil {
		t.Fatalf("list the persons after the refused add: %v", listErr)
	}

	if len(after) != len(persons) {
		t.Fatalf("persons = %d, want %d: the refused add stored an account", len(after), len(persons))
	}

	if got := f.entries(t, mfasvc.ActionPasskeyAdded); len(got) != 0 {
		t.Fatalf("passkey add entries = %d, want 0", len(got))
	}
}

// newPersonInput returns the input of a new break-glass person with subject.
func newPersonInput(subject string) principal.Input {
	return principal.Input{Subject: subject, DisplayName: "Break glass two", Kind: principal.KindPerson}
}

// listPersons returns the persons of the store of f.
func listPersons(t *testing.T, f *fixture) []principal.Principal {
	t.Helper()

	persons, _, err := f.store.ListPrincipals(t.Context(),
		principal.Query{After: "", Low: "", High: "", Limit: 100, InstallationOnly: false}, principal.KindPerson)
	if err != nil {
		t.Fatalf("list the persons: %v", err)
	}

	return persons
}

// wantOneAddRefusal proves that the add of a break-glass person was refused
// with one denial entry whose cause is cause, and that the store holds no new
// person, no passkey of the new credential and no passkey success entry.
func wantOneAddRefusal(t *testing.T, f *fixture, before int, cause string) {
	t.Helper()

	refusals := f.entries(t, mfasvc.ActionAddRefused)
	if len(refusals) != 1 {
		t.Fatalf("refusal entries = %d, want 1", len(refusals))
	}

	if changes := refusals[0].Changes; len(changes) != 1 || changes[0].New != cause {
		t.Fatalf("refusal changes = %+v, want the cause %q", changes, cause)
	}

	if after := listPersons(t, f); len(after) != before {
		t.Fatalf("persons = %d, want %d: the refused add stored a person", len(after), before)
	}

	_, keyErr := f.store.PasskeyByCredentialID(t.Context(), []byte("credential-new"))
	if !errors.Is(keyErr, mfa.ErrNotFound) {
		t.Fatalf("passkey after the refused add: err = %v, want ErrNotFound", keyErr)
	}

	if got := f.entries(t, mfasvc.ActionPasskeyAdded); len(got) != 0 {
		t.Fatalf("passkey add entries = %d, want 0", len(got))
	}
}

// TestBreakGlassAddPersonGuards proves that AddBreakGlassPerson refuses a call
// with no caller, a person that is not valid and a first passkey that is not
// valid. Each refusal writes one denial entry and stores no person and no
// passkey. Each case uses its own fixture, so the entry count is exact.
func TestBreakGlassAddPersonGuards(t *testing.T) {
	t.Parallel()

	t.Run("no caller", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		before := len(listPersons(t, f))
		in := newPersonInput("break-glass-two")

		_, _, err := f.svc.AddBreakGlassPerson(t.Context(), actor("", ""), in, newPasskey("new"))
		f.wantRefusal(t, "add a person with no caller", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)
		wantOneAddRefusal(t, f, before, "no caller")
	})

	t.Run("person not valid", func(t *testing.T) {
		t.Parallel()

		for _, subject := range []string{"", "break glass two", "break-glass\nlf"} {
			f := newBreakGlassFixture(t)
			before := len(listPersons(t, f))
			in := principal.Input{Subject: subject, DisplayName: "Break glass two", Kind: principal.KindPerson}

			_, _, err := f.svc.AddBreakGlassPerson(t.Context(), actor(glass, "ses-glass"), in, newPasskey("new"))
			f.wantRefusal(t, "add a person with subject "+subject, err, principal.ErrInvalid, mfasvc.ActionAddRefused)
			wantOneAddRefusal(t, f, before, "the person is not valid")
		}
	})

	t.Run("first passkey not valid", func(t *testing.T) {
		t.Parallel()

		for _, tc := range invalidPasskeys() {
			f := newBreakGlassFixture(t)
			before := len(listPersons(t, f))
			in := newPersonInput("break-glass-two")

			next := newPasskey("new")
			tc.mutate(&next)

			_, _, err := f.svc.AddBreakGlassPerson(t.Context(), actor(glass, "ses-glass"), in, next)
			f.wantRefusal(t, "add a person with "+tc.name, err, mfa.ErrInvalid, mfasvc.ActionAddRefused)
			wantOneAddRefusal(t, f, before, "the passkey is not valid")
		}
	})
}
