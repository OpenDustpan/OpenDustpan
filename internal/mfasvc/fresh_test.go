package mfasvc_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/mfasvc"
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
			t.Fatalf("BeginRegistration with no fresh step: err = nil, want a refusal")
		}

		if got := f.entries(t, mfasvc.ActionAddRefused); len(got) == 0 {
			t.Fatalf("BeginRegistration with no fresh step: no refusal entry with the action %s",
				mfasvc.ActionAddRefused)
		}

		after, err := f.svc.Passkeys(t.Context(), a)
		if err != nil || len(after) != len(before) {
			t.Fatalf("passkeys after = %d, %v; want %d unchanged", len(after), err, len(before))
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
			t.Fatalf("RemovePasskey with no fresh step: err = nil, want a refusal")
		}

		if got := f.entries(t, mfasvc.ActionRemoveRefused); len(got) == 0 {
			t.Fatalf("RemovePasskey with no fresh step: no refusal entry with the action %s",
				mfasvc.ActionRemoveRefused)
		}

		if removed := f.entries(t, mfasvc.ActionPasskeyRemoved); len(removed) != 0 {
			t.Fatalf("removed entries = %d, want 0", len(removed))
		}

		keys, err := f.svc.Passkeys(t.Context(), a)
		if err != nil || len(keys) != 1 {
			t.Fatalf("passkeys after = %d, %v; want the passkey kept", len(keys), err)
		}
	})
}
