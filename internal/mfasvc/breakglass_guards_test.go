package mfasvc_test

import (
	"bytes"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/mfa"
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
