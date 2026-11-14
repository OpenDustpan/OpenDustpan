package mfasvc_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	externalURL = "https://dustpan.example.com"
	rpID        = "dustpan.example.com"
	alice       = "prn-alice"
	bob         = "prn-bob"
)

// clock is a time that a test moves.
type clock struct {
	at time.Time
	mu sync.Mutex
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

// fixture is a service over a SQLite store, the go-webauthn adapter and a
// software authenticator.
type fixture struct {
	store *sqlite.Store
	svc   *mfasvc.Service
	auth  *webauthntest.Authenticator
	clock *clock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	for _, id := range []string{alice, bob} {
		p := principal.Principal{
			CreatedAt: time.Now(), UpdatedAt: time.Now(), DisabledAt: time.Time{}, ID: id, Subject: id,
			DisplayName: id, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
			GroupCount: 0, BreakGlass: false,
		}

		if createErr := st.CreatePrincipal(t.Context(), p, base(time.Now(), principal.Ref(id))); createErr != nil {
			t.Fatalf("create principal %s: %v", id, createErr)
		}
	}

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	c := &clock{at: time.Date(2026, time.November, 5, 9, 0, 0, 0, time.UTC), mu: sync.Mutex{}}

	svc, err := mfasvc.New(mfasvc.Config{Store: st, RP: rp, Audit: st, Now: c.now, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return &fixture{store: st, svc: svc, auth: webauthntest.New(rpID, externalURL), clock: c}
}

// base returns the entry that an HTTP adapter fills for a caller.
func base(at time.Time, object string) audit.Entry {
	return audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "alice", IDPSubject: "", TokenID: "", Action: "test",
		Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "mfasvc-test", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
		PrincipalKind: "", JobID: "",
	}
}

// actor returns the caller id in the session session.
func actor(id, session string) mfasvc.Actor {
	return mfasvc.Actor{Entry: base(time.Now(), "x"), PrincipalID: id, SessionID: session}
}

// anonymous returns the caller of a passkey sign-in.
func anonymous() mfasvc.Actor {
	return mfasvc.Actor{Entry: base(time.Now(), "x"), PrincipalID: "", SessionID: ""}
}

// personOf returns the WebAuthn person of id.
func personOf(id string) mfa.Person {
	return mfa.Person{ID: id, Name: strings.TrimPrefix(id, "prn-"), DisplayName: id}
}

// register runs a register ceremony of id and returns the credential and
// the stored passkey.
func (f *fixture) register(t *testing.T, id, name string) (*webauthntest.Credential, mfa.Passkey) {
	t.Helper()

	a := actor(id, "ses-"+id)

	options, err := f.svc.BeginRegistration(t.Context(), a, personOf(id))
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}

	c, err := webauthntest.ChallengeOf(options)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}

	cred, err := webauthntest.NewCredential([]byte(id), true)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	response, err := f.auth.Register(cred, webauthntest.For(c))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	key, err := f.svc.FinishRegistration(t.Context(), a, personOf(id), name, response)
	if err != nil {
		t.Fatalf("finish registration: %v", err)
	}

	return cred, key
}

// step is the caller, the kind and the binding of an assertion ceremony.
type step struct {
	a    mfasvc.Actor
	b    mfasvc.Binding
	kind mfa.Ceremony
}

// signIn is the step of a passkey sign-in.
func signIn() step {
	return step{a: anonymous(), b: mfasvc.Binding{PersonID: "", PendingID: ""}, kind: mfa.CeremonySignIn}
}

// fresh is the step of a fresh authentication of alice in her session.
func fresh() step {
	return step{a: actor(alice, "ses-alice"), b: mfasvc.Binding{PersonID: "", PendingID: ""}, kind: mfa.CeremonyFresh}
}

// second is the step of a second factor of alice in the pending sign-in.
func second(pending string) step {
	return step{a: anonymous(), b: mfasvc.Binding{PersonID: alice, PendingID: pending}, kind: mfa.CeremonySecondFactor}
}

// finish ends the ceremony of st with the response.
func (f *fixture) finish(t *testing.T, st step, response []byte) (mfa.Passkey, error) {
	t.Helper()

	return f.svc.FinishAssertion(t.Context(), st.a, st.kind, st.b, response)
}

// same is the change of a ceremony that changes nothing.
func same(c webauthntest.Ceremony) webauthntest.Ceremony { return c }

// counter returns the change of a ceremony that sets the counter n.
func counter(n uint32) func(webauthntest.Ceremony) webauthntest.Ceremony {
	return func(c webauthntest.Ceremony) webauthntest.Ceremony { return c.WithCounter(n) }
}

// assertion starts the ceremony of st, and returns the response of cred with
// the challenge of the ceremony and the change of the values.
func (f *fixture) assertion(
	t *testing.T, st step, cred *webauthntest.Credential, change func(webauthntest.Ceremony) webauthntest.Ceremony,
) []byte {
	t.Helper()

	options, err := f.svc.BeginAssertion(t.Context(), st.a, st.kind, st.b)
	if err != nil {
		t.Fatalf("begin %s: %v", st.kind, err)
	}

	c, err := webauthntest.ChallengeOf(options)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}

	response, err := f.auth.Assert(cred, change(webauthntest.For(c)))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	return response
}

// entries returns the entries of @server with the action.
func (f *fixture) entries(t *testing.T, action string) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == action {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// wantRefusal fails the test unless err is want and the newest entry with
// the action is a refusal: a failure for mfa.ErrUnavailable, and a denial
// for each other error.
func (f *fixture) wantRefusal(t *testing.T, what string, err, want error, action string) {
	t.Helper()

	outcome := audit.OutcomeDenied
	if errors.Is(want, mfa.ErrUnavailable) {
		outcome = audit.OutcomeFailure
	}

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}

	es := f.entries(t, action)
	if len(es) == 0 {
		t.Fatalf("%s: no entry with the action %s", what, action)
	}

	if es[0].Outcome != outcome {
		t.Fatalf("%s: entry outcome = %s, want %s", what, es[0].Outcome, outcome)
	}
}

func TestNewNeedsEachDependency(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	cases := []mfasvc.Config{
		{Store: nil, RP: rp, Audit: f.store, Now: nil, Log: nil},
		{Store: f.store, RP: nil, Audit: f.store, Now: nil, Log: nil},
		{Store: f.store, RP: rp, Audit: nil, Now: nil, Log: nil},
	}

	for i, c := range cases {
		if _, err := mfasvc.New(c); !errors.Is(err, mfasvc.ErrMissingDependency) {
			t.Errorf("config %d: err = %v, want ErrMissingDependency", i, err)
		}
	}
}

// TestRegisterAndUseEachKind adds a passkey and uses it for a sign-in, a
// second factor and a fresh step.
func TestRegisterAndUseEachKind(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, key := f.register(t, alice, "laptop")

	if key.PersonID != alice || key.Name != "laptop" || !strings.HasPrefix(key.ID, mfa.PasskeyIDPrefix) {
		t.Fatalf("passkey = %+v, want the laptop of alice", key)
	}

	if added := f.entries(t, mfasvc.ActionPasskeyAdded); len(added) != 1 || added[0].Object != mfasvc.Object(key.ID) {
		t.Fatalf("added entries = %+v, want one about %s", added, key.ID)
	}

	steps := []step{signIn(), second("psi-1"), fresh()}

	for i := range steps {
		response := f.assertion(t, steps[i], cred, same)

		got, err := f.finish(t, steps[i], response)
		if err != nil {
			t.Fatalf("%s: finish: %v", steps[i].kind, err)
		}

		if got.ID != key.ID || got.SignCount != cred.Counter || got.LastUsedAt.IsZero() {
			t.Fatalf("%s: passkey = %+v, want %s with counter %d", steps[i].kind, got, key.ID, cred.Counter)
		}
	}
}

// TestReplayedChallenge sends one response two times, and a good response
// after a failed one with the same challenge. A challenge works once, also
// when its first use failed a check.
func TestReplayedChallenge(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, _ := f.register(t, alice, "laptop")

	response := f.assertion(t, signIn(), cred, same)

	if _, err := f.finish(t, signIn(), response); err != nil {
		t.Fatalf("first use: %v", err)
	}

	_, err := f.finish(t, signIn(), response)
	f.wantRefusal(t, "replayed assertion", err, mfa.ErrNotFound, mfasvc.ActionCeremonyRefused)

	options, err := f.svc.BeginAssertion(t.Context(), anonymous(), mfa.CeremonySignIn, signIn().b)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	c, err := webauthntest.ChallengeOf(options)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}

	bad, err := f.auth.Assert(cred, webauthntest.For(c).WithBadSignature())
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	_, err = f.finish(t, signIn(), bad)
	f.wantRefusal(t, "bad signature", err, mfa.ErrVerification, mfasvc.ActionCeremonyRefused)

	got := f.entries(t, mfasvc.ActionCeremonyRefused)
	if len(got) == 0 || got[0].Kind != audit.KindSecurity {
		t.Fatalf("entries of a bad signature = %+v, want a security entry first", got)
	}

	good, err := f.auth.Assert(cred, webauthntest.For(c))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	_, err = f.finish(t, signIn(), good)
	f.wantRefusal(t, "good response after a failed use", err, mfa.ErrNotFound, mfasvc.ActionCeremonyRefused)
}

// begin starts a register ceremony of alice in her session and returns
// the response of a new credential.
func (f *fixture) begin(t *testing.T) []byte {
	t.Helper()

	options, err := f.svc.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(alice))
	if err != nil {
		t.Fatalf("begin: %v", err)
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

	return response
}

// TestReplayedRegistration sends one registration response two times.
func TestReplayedRegistration(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	a := actor(alice, "ses-alice")
	response := f.begin(t)

	if _, err := f.svc.FinishRegistration(t.Context(), a, personOf(alice), "first", response); err != nil {
		t.Fatalf("first finish: %v", err)
	}

	_, err := f.svc.FinishRegistration(t.Context(), a, personOf(alice), "second", response)
	f.wantRefusal(t, "replayed registration", err, mfa.ErrNotFound, mfasvc.ActionAddRefused)

	keys, err := f.svc.Passkeys(t.Context(), a)
	if err != nil || len(keys) != 1 {
		t.Fatalf("passkeys = %d, %v; want 1", len(keys), err)
	}
}

// TestCounterRegressionSuspends uses a passkey with a counter that goes
// back. The store suspends the passkey and writes a security entry, and each
// later use fails.
func TestCounterRegressionSuspends(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, key := f.register(t, alice, "laptop")

	response := f.assertion(t, fresh(), cred, counter(10))
	if _, err := f.finish(t, fresh(), response); err != nil {
		t.Fatalf("use with counter 10: %v", err)
	}

	response = f.assertion(t, fresh(), cred, counter(7))

	_, err := f.finish(t, fresh(), response)
	if !errors.Is(err, mfa.ErrCounterRegressed) {
		t.Fatalf("use with counter 7: err = %v, want ErrCounterRegressed", err)
	}

	suspended := f.entries(t, mfasvc.ActionPasskeySuspended)
	if len(suspended) != 1 || suspended[0].Kind != audit.KindSecurity || suspended[0].Object != mfasvc.Object(key.ID) {
		t.Fatalf("suspension entries = %+v, want one security entry about %s", suspended, key.ID)
	}

	stored, err := f.store.PasskeyByCredentialID(t.Context(), key.CredentialID)
	if err != nil || stored.SuspendedAt.IsZero() || stored.SignCount != 10 {
		t.Fatalf("stored passkey = %+v, %v; want suspended with counter 10", stored, err)
	}

	_, err = f.svc.BeginAssertion(t.Context(), fresh().a, mfa.CeremonyFresh, fresh().b)
	f.wantRefusal(t, "fresh step with only a suspended passkey", err, mfa.ErrNotFound, mfasvc.ActionCeremonyRefused)

	response = f.assertion(t, signIn(), cred, counter(50))

	_, err = f.finish(t, signIn(), response)
	f.wantRefusal(t, "sign-in with a suspended passkey", err, mfa.ErrSuspended, mfasvc.ActionCeremonyRefused)
}

// ownerCase is one case of TestCeremonyBelongsToTheCaller.
type ownerCase struct {
	run  func(t *testing.T, f *fixture, aliceCred, bobCred *webauthntest.Credential)
	name string
}

// TestCeremonyBelongsToTheCaller checks the authorization at the point of
// use: a ceremony, a challenge and a passkey of another person or session
// fail, and the failed step uses up the challenge.
func TestCeremonyBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	cases := []ownerCase{
		{name: "a registration of another person", run: ownRegistrationBegin},
		{name: "a registration finished in another session", run: ownRegistrationFinish},
		{name: "a second factor of another pending sign-in", run: ownPending},
		{name: "a passkey of another person as a second factor", run: ownSecondFactorKey},
		{name: "a fresh step of another session", run: ownFreshSession},
		{name: "a sign-in challenge used for a fresh step", run: ownKind},
		{name: "a sign-in with the user handle of another person", run: ownUserHandle},
		{name: "a removal of a passkey of another person", run: ownRemoval},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// Each subtest makes its own fixture and credentials: Assert
			// changes the counter of the credential that it uses.
			f := newFixture(t)
			aliceCred, _ := f.register(t, alice, "laptop")
			bobCred, _ := f.register(t, bob, "phone")

			c.run(t, f, aliceCred, bobCred)
		})
	}
}

func ownRegistrationBegin(t *testing.T, f *fixture, _, _ *webauthntest.Credential) {
	t.Helper()

	_, err := f.svc.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(bob))
	f.wantRefusal(t, "begin for another person", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)

	_, err = f.svc.BeginRegistration(t.Context(), actor(alice, ""), personOf(alice))
	f.wantRefusal(t, "begin with no session", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)
}

func ownRegistrationFinish(t *testing.T, f *fixture, _, _ *webauthntest.Credential) {
	t.Helper()

	response := f.begin(t)

	_, err := f.svc.FinishRegistration(t.Context(), actor(alice, "ses-other"), personOf(alice), "key", response)
	f.wantRefusal(t, "finish in another session", err, mfa.ErrWrongPerson, mfasvc.ActionAddRefused)

	_, err = f.svc.FinishRegistration(t.Context(), actor(alice, "ses-alice"), personOf(alice), "key", response)
	f.wantRefusal(t, "finish after the refusal", err, mfa.ErrNotFound, mfasvc.ActionAddRefused)
}

func ownPending(t *testing.T, f *fixture, aliceCred, _ *webauthntest.Credential) {
	t.Helper()

	response := f.assertion(t, second("psi-1"), aliceCred, same)

	_, err := f.finish(t, second("psi-2"), response)
	f.wantRefusal(t, "second factor of another pending sign-in", err, mfa.ErrWrongPerson,
		mfasvc.ActionCeremonyRefused)
}

func ownSecondFactorKey(t *testing.T, f *fixture, _, bobCred *webauthntest.Credential) {
	t.Helper()

	response := f.assertion(t, second("psi-3"), bobCred, same)

	_, err := f.finish(t, second("psi-3"), response)
	f.wantRefusal(t, "passkey of bob for alice", err, mfa.ErrWrongPerson, mfasvc.ActionCeremonyRefused)
}

func ownFreshSession(t *testing.T, f *fixture, aliceCred, _ *webauthntest.Credential) {
	t.Helper()

	response := f.assertion(t, fresh(), aliceCred, same)
	stolen := fresh()
	stolen.a.SessionID = "ses-stolen"

	_, err := f.finish(t, stolen, response)
	f.wantRefusal(t, "fresh step of another session", err, mfa.ErrWrongPerson, mfasvc.ActionCeremonyRefused)
}

func ownKind(t *testing.T, f *fixture, aliceCred, _ *webauthntest.Credential) {
	t.Helper()

	response := f.assertion(t, signIn(), aliceCred, same)

	_, err := f.finish(t, fresh(), response)
	f.wantRefusal(t, "sign-in challenge as a fresh step", err, mfa.ErrWrongPerson, mfasvc.ActionCeremonyRefused)
}

func ownUserHandle(t *testing.T, f *fixture, aliceCred, _ *webauthntest.Credential) {
	t.Helper()

	response := f.assertion(t, signIn(), aliceCred, func(c webauthntest.Ceremony) webauthntest.Ceremony {
		return c.WithUserHandle([]byte(bob))
	})

	_, err := f.finish(t, signIn(), response)
	f.wantRefusal(t, "user handle of bob", err, mfa.ErrWrongPerson, mfasvc.ActionCeremonyRefused)
}

func ownRemoval(t *testing.T, f *fixture, _, _ *webauthntest.Credential) {
	t.Helper()

	keys, err := f.svc.Passkeys(t.Context(), actor(bob, "ses-bob"))
	if err != nil || len(keys) != 1 {
		t.Fatalf("passkeys of bob = %d, %v", len(keys), err)
	}

	err = f.svc.RemovePasskey(t.Context(), actor(alice, "ses-alice"), keys[0].ID)
	f.wantRefusal(t, "remove a passkey of bob", err, mfa.ErrNotFound, mfasvc.ActionRemoveRefused)
}

// TestListRefusesAnEmptyCaller proves that a list for a caller with no person
// is refused and has an audit entry.
func TestListRefusesAnEmptyCaller(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.svc.Passkeys(t.Context(), actor("", "ses-none"))
	f.wantRefusal(t, "list with no caller", err, mfa.ErrWrongPerson, mfasvc.ActionListRefused)
}

// TestBoundaryRefusesBadInput sends hostile input to the boundary of each
// step: a name that the rules refuse, a response that is too large or is no
// JSON, and a passkey ID that no passkey can have.
func TestBoundaryRefusesBadInput(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	a := actor(alice, "ses-alice")

	for _, name := range []string{"", "my key", "key\r\nforged: yes", strings.Repeat("k", 101), "-key", "ключ"} {
		_, err := f.svc.FinishRegistration(t.Context(), a, personOf(alice), name, []byte("{}"))
		f.wantRefusal(t, "name "+strconv.Quote(name), err, mfa.ErrInvalid, mfasvc.ActionAddRefused)
	}

	for _, response := range [][]byte{
		nil, []byte("{"), []byte(`{"rawId":"YQ"}`),
		[]byte(strings.Repeat("a", mfa.MaxResponseBytes+1)),
	} {
		_, err := f.finish(t, signIn(), response)
		f.wantRefusal(t, "bad response", err, mfa.ErrInvalid, mfasvc.ActionCeremonyRefused)
	}

	for _, id := range []string{"", "pky-1\r\nX", "../pky-1", strings.Repeat("p", 200)} {
		err := f.svc.RemovePasskey(t.Context(), a, id)
		f.wantRefusal(t, "remove "+strconv.Quote(id), err, mfa.ErrNotFound, mfasvc.ActionRemoveRefused)
	}

	for _, k := range []mfa.Ceremony{mfa.CeremonyRegister, "login"} {
		_, err := f.svc.BeginAssertion(t.Context(), a, k, signIn().b)
		f.wantRefusal(t, "assertion of "+string(k), err, mfa.ErrInvalid, mfasvc.ActionCeremonyRefused)
	}

	for _, e := range f.entries(t, mfasvc.ActionAddRefused) {
		if strings.ContainsAny(e.Object, "\r\n") || strings.Contains(e.Object, "forged") {
			t.Fatalf("refusal entry %+v holds the hostile name", e)
		}
	}
}

// TestLimitOfPasskeys adds MaxPasskeys passkeys. A person at the limit gets
// no ceremony.
func TestLimitOfPasskeys(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for n := range mfa.MaxPasskeys {
		f.register(t, alice, "key-"+strconv.Itoa(n))
	}

	_, err := f.svc.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(alice))
	f.wantRefusal(t, "a ceremony above the limit", err, mfa.ErrLimit, mfasvc.ActionAddRefused)
}

// TestChallengeExpires finishes a ceremony after ChallengeLifetime.
func TestChallengeExpires(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cred, _ := f.register(t, alice, "laptop")

	response := f.assertion(t, signIn(), cred, same)
	f.clock.add(mfa.ChallengeLifetime)

	_, err := f.finish(t, signIn(), response)
	f.wantRefusal(t, "a ceremony at its expiry", err, mfa.ErrNotFound, mfasvc.ActionCeremonyRefused)
}

// TestRemovePasskey removes the own passkey, and keeps the last one of a
// break-glass person.
func TestRemovePasskey(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, key := f.register(t, alice, "laptop")
	a := actor(alice, "ses-alice")

	if err := f.svc.RemovePasskey(t.Context(), a, key.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if removed := f.entries(t, mfasvc.ActionPasskeyRemoved); len(removed) != 1 {
		t.Fatalf("removed entries = %d, want 1", len(removed))
	}

	err := f.svc.RemovePasskey(t.Context(), mfasvc.Actor{Entry: a.Entry, PrincipalID: "", SessionID: ""}, key.ID)
	f.wantRefusal(t, "remove with no caller", err, mfa.ErrWrongPerson, mfasvc.ActionRemoveRefused)
}

// failingStore is a store that cannot answer.
type failingStore struct {
	mfa.PasskeyStore
}

func (failingStore) Passkeys(context.Context, string) ([]mfa.Passkey, error) {
	return nil, mfa.ErrUnavailable
}

func (failingStore) CreateChallenge(context.Context, mfa.Challenge, mfa.Digest) error {
	return mfa.ErrUnavailable
}

func (failingStore) TakeChallenge(context.Context, mfa.Digest, time.Time) (mfa.Challenge, error) {
	return mfa.Challenge{}, mfa.ErrUnavailable
}

// failingAuditor refuses each append.
type failingAuditor struct{}

var errAppend = errors.New("append refused")

func (failingAuditor) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAppend
}

// TestFailsClosed checks that a store that cannot answer refuses each step
// with an audit entry of the failure, and that a refusal whose entry cannot
// be written still refuses.
func TestFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: failingStore{f.store}, RP: rp, Audit: f.store, Now: nil, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, err = svc.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(alice))
	f.wantRefusal(t, "begin with no store", err, mfa.ErrUnavailable, mfasvc.ActionAddRefused)

	_, err = svc.BeginAssertion(t.Context(), anonymous(), mfa.CeremonySignIn, signIn().b)
	f.wantRefusal(t, "sign-in with no store", err, mfa.ErrUnavailable, mfasvc.ActionCeremonyRefused)

	cred, err := webauthntest.NewCredential([]byte(alice), false)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	response, err := f.auth.Assert(cred, webauthntest.For(make([]byte, mfa.ChallengeBytes)))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	_, err = svc.FinishAssertion(t.Context(), anonymous(), mfa.CeremonySignIn, signIn().b, response)
	f.wantRefusal(t, "finish with no store", err, mfa.ErrUnavailable, mfasvc.ActionCeremonyRefused)

	quiet, err := mfasvc.New(mfasvc.Config{Store: f.store, RP: rp, Audit: failingAuditor{}, Now: nil, Log: nil})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, err = quiet.BeginRegistration(t.Context(), actor(alice, "ses-alice"), personOf(bob))
	if !errors.Is(err, mfa.ErrWrongPerson) || !errors.Is(err, errAppend) {
		t.Fatalf("refusal with no audit log: err = %v, want ErrWrongPerson joined with the append error", err)
	}
}
