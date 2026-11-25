package mfasvc_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/mfa"
)

// TestFactorChangeNeedsFreshActor checks the register and the removal of a
// passkey for a caller with a session and no fresh step, with the setting
// off. The service must refuse, write a refusal entry, and change nothing in
// the store, also when the handler did not check.
//
// Defense in depth: the handler checks the fresh step too. This test sends
// the caller to the service alone.
func TestFactorChangeNeedsFreshActor(t *testing.T) {
	t.Parallel()

	t.Run("register", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		f.register(t, alice, "laptop")

		// The caller has a session and no fresh step: the Actor has no fact
		// that a fresh step happened.
		a := actor(alice, "ses-alice")
		a.Fresh = false

		before, err := f.svc.Passkeys(t.Context(), a)
		if err != nil {
			t.Fatalf("passkeys before: %v", err)
		}

		_, err = f.svc.BeginRegistration(t.Context(), a, personOf(alice))
		if err == nil {
			t.Fatal("BeginRegistration with no fresh step: err = nil, want a refusal")
		}

		f.wantRefusal(t, "BeginRegistration with no fresh step", err, mfa.ErrFreshStep, mfasvc.ActionAddRefused)

		after, err := f.svc.Passkeys(t.Context(), a)
		if err != nil || len(after) != len(before) {
			t.Fatalf("passkeys after = %d, %v; want %d unchanged", len(after), err, len(before))
		}
	})

	t.Run("finish", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		// The begin call runs with a fresh step, so the challenge exists. The
		// finish call comes from a caller with no fresh step, which skips the
		// begin call's check.
		options, err := f.svc.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(alice))
		if err != nil {
			t.Fatalf("begin registration: %v", err)
		}

		c, err := webauthntest.ChallengeOf(options)
		if err != nil {
			t.Fatalf("challenge: %v", err)
		}

		cred, err := webauthntest.NewCredential([]byte(alice), true)
		if err != nil {
			t.Fatalf("credential: %v", err)
		}

		response, err := f.auth.Register(cred, webauthntest.For(c))
		if err != nil {
			t.Fatalf("register: %v", err)
		}

		a := actor(alice, "ses-alice")
		a.Fresh = false

		_, err = f.svc.FinishRegistration(t.Context(), a, personOf(alice), "laptop", response)
		if err == nil {
			t.Fatal("FinishRegistration with no fresh step: err = nil, want a refusal")
		}

		f.wantRefusal(t, "FinishRegistration with no fresh step", err, mfa.ErrFreshStep, mfasvc.ActionAddRefused)

		if added := f.entries(t, mfasvc.ActionPasskeyAdded); len(added) != 0 {
			t.Fatalf("added entries = %d, want 0", len(added))
		}
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, key := f.register(t, alice, "laptop")

		a := actor(alice, "ses-alice")
		a.Fresh = false

		err := f.svc.RemovePasskey(t.Context(), a, key.ID)
		if err == nil {
			t.Fatal("RemovePasskey with no fresh step: err = nil, want a refusal")
		}

		f.wantRefusal(t, "RemovePasskey with no fresh step", err, mfa.ErrFreshStep, mfasvc.ActionRemoveRefused)

		if removed := f.entries(t, mfasvc.ActionPasskeyRemoved); len(removed) != 0 {
			t.Fatalf("removed entries = %d, want 0", len(removed))
		}

		keys, err := f.svc.Passkeys(t.Context(), a)
		if err != nil || len(keys) != 1 {
			t.Fatalf("passkeys after = %d, %v; want the passkey kept", len(keys), err)
		}
	})
}
