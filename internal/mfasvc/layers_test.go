package mfasvc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
)

// countingRP is a relying party that counts the checks of a response and
// can drop the user verification of an assertion, as a changed adapter
// could. Each other step is the step of the real adapter.
type countingRP struct {
	mfa.RelyingParty

	creations  *atomic.Int64
	assertions *atomic.Int64
	dropUV     bool
}

func (r countingRP) VerifyCreation(p mfa.Person, c, response []byte) (mfa.Registration, error) {
	r.creations.Add(1)

	return r.RelyingParty.VerifyCreation(p, c, response)
}

func (r countingRP) VerifyAssertion(
	k mfa.Ceremony, owner mfa.Person, key mfa.Passkey, c, response []byte,
) (mfa.Assertion, error) {
	r.assertions.Add(1)

	got, err := r.RelyingParty.VerifyAssertion(k, owner, key, c, response)
	if r.dropUV {
		got.UserVerified = false
	}

	return got, err
}

// withRP returns a service over the store of f with the relying party rp.
func (f *fixture) withRP(t *testing.T, rp mfa.RelyingParty) *mfasvc.Service {
	t.Helper()

	svc, err := mfasvc.New(mfasvc.Config{Store: f.store, RP: rp, Audit: f.store, Now: f.clock.now, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// counting returns the real adapter in a countingRP.
func counting(t *testing.T, dropUV bool) countingRP {
	t.Helper()

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	return countingRP{RelyingParty: rp, creations: new(atomic.Int64), assertions: new(atomic.Int64), dropUV: dropUV}
}

// TestRegistrationRefusesAChallengeOfAnotherKind sends a registration with
// the challenge of a fresh step of the same person and session. Only the
// check of the kind in the service stops it, before any check of the
// response.
func TestRegistrationRefusesAChallengeOfAnotherKind(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.register(t, alice, "laptop")

	rp := counting(t, false)
	svc := f.withRP(t, rp)
	a := actor(alice, "ses-alice")

	options, err := svc.BeginAssertion(t.Context(), a, mfa.CeremonyFresh, fresh().b)
	if err != nil {
		t.Fatalf("begin a fresh step: %v", err)
	}

	c, err := webauthntest.ChallengeOf(options)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}

	cred, err := webauthntest.NewCredential([]byte(alice), false)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	response, err := f.auth.Register(cred, webauthntest.For(c))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = svc.FinishRegistration(t.Context(), a, personOf(alice), "second", response)
	f.wantRefusal(t, "registration with a fresh challenge", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)

	if n := rp.creations.Load(); n != 0 {
		t.Fatalf("checks of the response = %d, want 0", n)
	}
}

// emptyStore is a store whose take returns a register challenge with no
// person and no session, as a changed row could hold it.
type emptyStore struct {
	mfa.PasskeyStore
}

func (emptyStore) TakeChallenge(context.Context, mfa.Digest, time.Time) (mfa.Challenge, error) {
	return mfa.Challenge{
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Kind: mfa.CeremonyRegister,
		PersonID: "", PendingID: "", SessionID: "",
	}, nil
}

// TestRegistrationRefusesEmptyIDs calls the check of the empty person and
// session of the service alone: the challenge, the caller and the person
// all have no ID, so each comparison of IDs passes.
func TestRegistrationRefusesEmptyIDs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	rp := counting(t, false)

	svc, err := mfasvc.New(mfasvc.Config{Store: emptyStore{f.store}, RP: rp, Audit: f.store, Now: nil, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	cred, err := webauthntest.NewCredential([]byte(alice), false)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	response, err := f.auth.Register(cred, webauthntest.For(make([]byte, mfa.ChallengeBytes)))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	nobody := mfasvc.Actor{Entry: base(time.Now(), "x"), PrincipalID: "", SessionID: ""}

	_, err = svc.FinishRegistration(t.Context(), nobody, mfa.Person{ID: "", Name: "x", DisplayName: ""}, "key",
		response)
	f.wantRefusal(t, "registration with no IDs", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)

	if n := rp.creations.Load(); n != 0 {
		t.Fatalf("checks of the response = %d, want 0", n)
	}
}

// TestSuspendedPasskeyIsRefusedBeforeTheCheck uses a suspended passkey for a
// sign-in. The service refuses it before the relying party checks the
// response.
func TestSuspendedPasskeyIsRefusedBeforeTheCheck(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, _ := f.register(t, alice, "laptop")

	response := f.assertion(t, fresh(), cred, counter(10))
	if _, err := f.finish(t, fresh(), response); err != nil {
		t.Fatalf("use with counter 10: %v", err)
	}

	response = f.assertion(t, fresh(), cred, counter(7))
	if _, err := f.finish(t, fresh(), response); !errors.Is(err, mfa.ErrCounterRegressed) {
		t.Fatalf("use with counter 7: err = %v, want ErrCounterRegressed", err)
	}

	rp := counting(t, false)
	svc := f.withRP(t, rp)
	response = f.assertion(t, signIn(), cred, counter(50))

	_, err := svc.FinishAssertion(t.Context(), signIn().a, mfa.CeremonySignIn, signIn().b, response)
	f.wantRefusal(t, "sign-in with a suspended passkey", err, mfa.ErrSuspended, mfasvc.ActionCeremonyRefused)

	if n := rp.assertions.Load(); n != 0 {
		t.Fatalf("checks of the response = %d, want 0", n)
	}
}

// TestUserVerificationIsCheckedAfterTheRelyingParty calls the check of the
// user verification of the service alone: the relying party passes each
// response and reports no user verification. A sign-in and a fresh step
// fail and record no use. A second factor, which prefers user verification,
// passes.
func TestUserVerificationIsCheckedAfterTheRelyingParty(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, key := f.register(t, alice, "laptop")
	svc := f.withRP(t, counting(t, true))

	for _, st := range []step{signIn(), fresh()} {
		response := f.assertion(t, st, cred, same)

		_, err := svc.FinishAssertion(t.Context(), st.a, st.kind, st.b, response)
		f.wantRefusal(t, string(st.kind)+" with no user verification", err, mfa.ErrVerification,
			mfasvc.ActionCeremonyRefused)
	}

	stored, err := f.store.PasskeyByCredentialID(t.Context(), key.CredentialID)
	if err != nil || !stored.LastUsedAt.IsZero() {
		t.Fatalf("stored passkey = %+v, %v; want no use", stored, err)
	}

	st := second("psi-1")
	response := f.assertion(t, st, cred, same)

	if _, err := svc.FinishAssertion(t.Context(), st.a, st.kind, st.b, response); err != nil {
		t.Fatalf("second factor with no user verification: %v", err)
	}
}
