package mfasvc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// entryCount returns the number of entries of @server, of every action.
func (f *fixture) entryCount(t *testing.T) int {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	return len(listing.Entries)
}

// TestNilActorIsRefused proves that each entry point of the service refuses a
// call with a nil Actor with ErrMissingActor. The HTTP adapter always passes an
// Actor, so this check is a defense-in-depth layer: a new caller that passes
// nil must still stop before any store or audit write.
func TestNilActorIsRefused(t *testing.T) {
	t.Parallel()

	// The zero values are enough: the Actor check runs before any field is read.
	var (
		person  mfa.Person
		binding mfasvc.Binding
	)

	calls := []struct {
		name string
		call func(f *fixture) error
	}{
		{name: "BeginRegistration", call: func(f *fixture) error {
			_, err := f.svc.BeginRegistration(t.Context(), nil, person)
			return err
		}},
		{name: "FinishRegistration", call: func(f *fixture) error {
			_, err := f.svc.FinishRegistration(t.Context(), nil, person, "", nil)
			return err
		}},
		{name: "BeginAssertion", call: func(f *fixture) error {
			_, err := f.svc.BeginAssertion(t.Context(), nil, mfa.Ceremony(""), binding)
			return err
		}},
		{name: "FinishAssertion", call: func(f *fixture) error {
			_, err := f.svc.FinishAssertion(t.Context(), nil, mfa.Ceremony(""), binding, nil)
			return err
		}},
		{name: "FinishSignIn", call: func(f *fixture) error {
			_, err := f.svc.FinishSignIn(t.Context(), nil, nil)
			return err
		}},
		{name: "Passkeys", call: func(f *fixture) error {
			_, err := f.svc.Passkeys(t.Context(), nil)
			return err
		}},
		{name: "RemovePasskey", call: func(f *fixture) error {
			return f.svc.RemovePasskey(t.Context(), nil, "pky-old")
		}},
		{name: "AddBreakGlassPerson", call: func(f *fixture) error {
			_, _, err := f.svc.AddBreakGlassPerson(t.Context(), nil,
				principal.Input{Subject: "break-glass-two", DisplayName: "Break glass two", Kind: principal.KindPerson},
				newPasskey("new"))
			return err
		}},
		{name: "RotatePasskey", call: func(f *fixture) error {
			return f.svc.RotatePasskey(t.Context(), nil, glass, "pky-old", newPasskey("new"))
		}},
	}

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newBreakGlassFixture(t)

			before := f.entryCount(t)

			if err := tc.call(f); !errors.Is(err, mfasvc.ErrMissingActor) {
				t.Fatalf("%s with a nil actor = %v, want ErrMissingActor", tc.name, err)
			}

			// A nil Actor has no entry base, so the refusal writes no entry.
			if after := f.entryCount(t); after != before {
				t.Fatalf("%s with a nil actor: entries = %d, want %d (no entry)", tc.name, after, before)
			}
		})
	}
}
