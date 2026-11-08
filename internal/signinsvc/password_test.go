package signinsvc_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// ldapID is the provider ID of the password tests.
const ldapID = "corp-ldap"

// lockLimit is the count of failures of one name from one source that sets
// a lock, as authgate.PasswordRule says.
const lockLimit = 10

// errDirectoryDown is the error of a directory that does not answer.
var errDirectoryDown = fmt.Errorf("%w: connection refused", session.ErrMethodUnavailable)

// fakeUser is one entry of the fake directory: the subject that the search
// finds for a typed name, and the password of a bind.
type fakeUser struct {
	subject  string
	password string
}

// fakeDirectory is a password method over a map of typed names. It finds the
// subject of a typed name, calls the gate, and then "sends" the password.
// Its options make it break the rules of the port, so that each check of
// the service has a method to stop.
type fakeDirectory struct {
	mu sync.Mutex

	users map[string]fakeUser
	kind  session.MethodKind
	// down makes each Verify fail as a directory that does not answer, after
	// the search.
	down bool
	// skipGate makes the method bind without a call of the gate.
	skipGate bool
	// gateSubject, when set, is the subject that the method gives the gate
	// instead of the subject that it found.
	gateSubject string
	// gateTwice makes the method call the gate a second time with another
	// subject.
	gateTwice bool
	// ignoreGate makes the method bind also when the gate refuses the bind.
	ignoreGate bool

	verifies int
	binds    []string
}

func newDirectory() *fakeDirectory {
	return &fakeDirectory{
		mu: sync.Mutex{}, kind: session.MethodLDAP, down: false, skipGate: false, gateSubject: "",
		gateTwice: false, ignoreGate: false, verifies: 0, binds: nil,
		users: map[string]fakeUser{
			"alice":       {subject: "sub-alice", password: "right"},
			"ALICE@corp":  {subject: "sub-alice", password: "right"},
			`corp\alice`:  {subject: "sub-alice", password: "right"},
			"alice.smith": {subject: "sub-alice", password: "right"},
			"bob":         {subject: "sub-bob", password: "right"},
		},
	}
}

func (d *fakeDirectory) Kind() session.MethodKind { return d.kind }

func (d *fakeDirectory) Verify(
	ctx context.Context, c session.Credentials, gate session.BindGate,
) (session.Assertion, error) {
	d.mu.Lock()
	d.verifies++
	u, found := d.users[c.UserName]
	d.mu.Unlock()

	var none session.Assertion

	if !found {
		return none, fmt.Errorf("%w: no entry", session.ErrMethodRejected)
	}

	if err := d.callGate(ctx, gate, u.subject); err != nil && !d.ignoresGate() {
		return none, fmt.Errorf("bind gate: %w", err)
	}

	d.mu.Lock()
	d.binds = append(d.binds, u.subject)
	down := d.down
	d.mu.Unlock()

	switch {
	case down:
		return none, errDirectoryDown
	case c.Password.Reveal() != u.password:
		return none, fmt.Errorf("%w: invalid credentials", session.ErrMethodRejected)
	default:
		return ldapAssertion(u.subject, c.UserName), nil
	}
}

// ignoresGate reports whether d binds also when the gate refuses.
func (d *fakeDirectory) ignoresGate() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.ignoreGate
}

// countingLocks is a lock store that counts the failures that the service
// adds.
type countingLocks struct {
	authgate.NameLockCounter

	mu   sync.Mutex
	adds int
}

func (c *countingLocks) AddNameFailure(ctx context.Context, f authgate.NameFailure) (authgate.NameLock, error) {
	c.mu.Lock()
	c.adds++
	c.mu.Unlock()

	return c.NameLockCounter.AddNameFailure(ctx, f)
}

// added returns the count of the failures that the service added so far.
func (c *countingLocks) added() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.adds
}

// callGate calls the gate as the options of d say.
func (d *fakeDirectory) callGate(ctx context.Context, gate session.BindGate, subject string) error {
	d.mu.Lock()
	skip, other, twice := d.skipGate, d.gateSubject, d.gateTwice
	d.mu.Unlock()

	if skip {
		return nil
	}

	checked := subject
	if other != "" {
		checked = other
	}

	if err := gate.BeforeBind(ctx, checked); err != nil {
		return err
	}

	if twice {
		return gate.BeforeBind(ctx, checked+"-other")
	}

	return nil
}

// calls is the number of Verify calls and of binds of a fake directory.
type calls struct {
	verifies int
	binds    int
}

// counts returns the calls of d so far.
func (d *fakeDirectory) counts() calls {
	d.mu.Lock()
	defer d.mu.Unlock()

	return calls{verifies: d.verifies, binds: len(d.binds)}
}

// ldapAssertion returns a valid Assertion of the provider ldapID.
func ldapAssertion(subject, userName string) session.Assertion {
	return session.Assertion{
		AuthTime: testNow(), ProviderID: ldapID, PrincipalID: "", Subject: subject, UserName: userName, Email: "",
		Method: session.MethodLDAP, Names: nil, Groups: nil, Factors: []session.Factor{session.FactorPassword},
		EmailVerified: false, GroupsPresent: false, AuthTimeKnown: true,
	}
}

// ldapProvider returns the LDAP provider of a route.
func ldapProvider() signinsvc.Provider {
	return signinsvc.Provider{ID: ldapID, Kind: session.MethodLDAP, AutoCreate: true, Stored: false}
}

// keyReader answers the key of the counts, or an error.
type keyReader struct {
	key []byte
	err error
}

func (k keyReader) NameLockKey(context.Context) ([]byte, error) { return k.key, k.err }

// passwordHarness is a service with the counts of a SQLite store, the fake
// ports of w, and a fake directory.
type passwordHarness struct {
	w     *world
	svc   *signinsvc.Service
	dir   *fakeDirectory
	locks *countingLocks
}

// newPasswordHarness returns a harness. keys replaces the key reader of the
// store when it is not nil.
func newPasswordHarness(t *testing.T, keys signinsvc.NameKeys) passwordHarness {
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

	if keys == nil {
		keys = st
	}

	locks := &countingLocks{NameLockCounter: st, mu: sync.Mutex{}, adds: 0}

	w := newWorld()
	w.principal.Subject = ldapID + ":sub-alice"

	svc, err := signinsvc.New(signinsvc.Config{
		Principals: w, Disabler: w, Generations: w, Sessions: w, Audit: w, TempAdmins: w, Groups: nil,
		Removals: nil, States: nil, Locks: locks, NameKeys: keys, Logger: slog.New(w.logs),
		OnAuditFailure: func() { w.auditFailed++ },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return passwordHarness{w: w, svc: svc, dir: newDirectory(), locks: locks}
}

// signIn signs in with the typed name and the password.
func (h passwordHarness) signIn(t *testing.T, name, password string) (signinsvc.Result, error) {
	t.Helper()

	return h.svc.CompletePassword(t.Context(), &signinsvc.PasswordRequest{
		Entry: callerEntry(), Provider: ldapProvider(), Method: h.dir, UserName: name,
		Password: session.NewPassword(password),
	})
}

// refusalReason returns the reason of a *RefusedError, or fails t.
func refusalReason(t *testing.T, err error) string {
	t.Helper()

	var refused *signinsvc.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a *RefusedError", err)
	}

	if refused.Answer != signinsvc.AnswerFailed {
		t.Fatalf(
			"answer of %q = %v, want AnswerFailed: a password refusal tells nothing",
			refused.Reason,
			refused.Answer,
		)
	}

	return refused.Reason
}

// failTimes makes n sign-ins of name with a wrong password, and fails t when
// one is not refused.
func (h passwordHarness) failTimes(t *testing.T, name string, n int) {
	t.Helper()

	for i := range n {
		if _, err := h.signIn(t, name, "wrong"); err == nil {
			t.Fatalf("wrong password %d of %s signed in", i+1, name)
		}
	}
}

// TestPasswordSignInStartsASession signs a person in through the fake
// directory: the session carries the provider and the method.
func TestPasswordSignInStartsASession(t *testing.T) {
	t.Parallel()

	h := newPasswordHarness(t, nil)

	res, err := h.signIn(t, "alice", "right")
	if err != nil {
		t.Fatalf("sign-in = %v, want a session", err)
	}

	if res.Method != session.MethodLDAP || res.Session.Provider != ldapID {
		t.Fatalf("result = %+v, want the method ldap and the provider %s", res, ldapID)
	}
}

// TestPasswordUnknownUserAndWrongPasswordLookTheSame proves that an unknown
// user and a wrong password get the same answer, the same reason and the
// same error class.
func TestPasswordUnknownUserAndWrongPasswordLookTheSame(t *testing.T) {
	t.Parallel()

	h := newPasswordHarness(t, nil)

	_, unknown := h.signIn(t, "ghost", "right")
	_, wrong := h.signIn(t, "alice", "wrong")

	if a, b := refusalReason(t, unknown), refusalReason(t, wrong); a != b || a != signinsvc.ReasonDirectoryRejected {
		t.Fatalf("reasons = %q and %q, want both %q", a, b, signinsvc.ReasonDirectoryRejected)
	}

	if !errors.Is(unknown, session.ErrMethodRejected) || !errors.Is(wrong, session.ErrMethodRejected) {
		t.Fatalf("errors = %v and %v, want both ErrMethodRejected", unknown, wrong)
	}
}

// TestPasswordNameLockStopsTheMethod proves that the typed name counts before
// Verify, also for a name that does not exist, and that a locked name never
// reaches the method: no search, no bind, also with the right password.
func TestPasswordNameLockStopsTheMethod(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ghost", "bob"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newPasswordHarness(t, nil)
			h.failTimes(t, name, lockLimit)

			before := h.dir.counts()

			_, err := h.signIn(t, name, "right")
			if got := refusalReason(t, err); got != signinsvc.ReasonNameLocked {
				t.Fatalf("sign-in of a locked name = %q, want %q", got, signinsvc.ReasonNameLocked)
			}

			if after := h.dir.counts(); after != before {
				t.Fatalf("a locked name made %d searches and %d binds, want none",
					after.verifies-before.verifies, after.binds-before.binds)
			}
		})
	}
}

// TestPasswordSubjectLockCountsEveryForm proves that each form of one user
// name counts on the person: three forms fail ten times together, so a
// fourth form with the right password is refused before the bind.
func TestPasswordSubjectLockCountsEveryForm(t *testing.T) {
	t.Parallel()

	h := newPasswordHarness(t, nil)

	h.failTimes(t, "alice", 4)
	h.failTimes(t, "ALICE@corp", 3)
	h.failTimes(t, `corp\alice`, 3)

	binds := h.dir.counts().binds

	_, err := h.signIn(t, "alice.smith", "right")
	if got := refusalReason(t, err); got != signinsvc.ReasonSubjectLocked {
		t.Fatalf("sign-in of a locked person = %q, want %q", got, signinsvc.ReasonSubjectLocked)
	}

	if b := h.dir.counts().binds; b != binds {
		t.Fatalf("a locked person got %d binds, want none: the password reached the directory", b-binds)
	}

	if !errors.Is(err, session.ErrMethodRejected) {
		t.Fatalf("err = %v, want the class of a wrong password", err)
	}

	// Another person is not locked: the password of bob reaches the bind.
	before := h.dir.counts().binds

	_, err = h.signIn(t, "bob", "wrong")
	if got := refusalReason(t, err); got != signinsvc.ReasonDirectoryRejected {
		t.Fatalf("sign-in of another person = %q, want %q", got, signinsvc.ReasonDirectoryRejected)
	}

	if after := h.dir.counts().binds; after != before+1 {
		t.Fatalf("another person got %d binds, want 1", after-before)
	}
}

// TestPasswordCountsClearOnlyAfterASession proves that a full sign-in clears
// the counts of the name and of the person, and that a directory that does
// not answer leaves them.
func TestPasswordCountsClearOnlyAfterASession(t *testing.T) {
	t.Parallel()

	t.Run("a session clears", func(t *testing.T) {
		t.Parallel()

		h := newPasswordHarness(t, nil)
		h.failTimes(t, "alice", lockLimit-1)

		if _, err := h.signIn(t, "alice", "right"); err != nil {
			t.Fatalf("sign-in = %v, want a session", err)
		}

		// Without the clear, the count would be past the limit now.
		h.failTimes(t, "alice", lockLimit-1)

		if _, err := h.signIn(t, "alice", "right"); err != nil {
			t.Fatalf("sign-in after the clear = %v, want a session", err)
		}
	})

	t.Run("a directory that does not answer keeps", func(t *testing.T) {
		t.Parallel()

		h := newPasswordHarness(t, nil)
		h.dir.down = true
		h.failTimes(t, "alice", lockLimit)
		h.dir.down = false

		_, err := h.signIn(t, "alice", "right")
		if got := refusalReason(t, err); got != signinsvc.ReasonNameLocked {
			t.Fatalf("sign-in after %d attempts that the directory did not answer = %q, want %q",
				lockLimit, got, signinsvc.ReasonNameLocked)
		}
	})
}

// TestPasswordMethodThatBreaksTheGateIsRefused gives the service methods that
// skip the gate, check another person, or check two persons. Each sign-in is
// refused, also with the right password.
func TestPasswordMethodThatBreaksTheGateIsRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(d *fakeDirectory)
		want   string
	}{
		{"no call of the gate", func(d *fakeDirectory) { d.skipGate = true }, signinsvc.ReasonGateSkipped},
		{
			"the gate checks another person", func(d *fakeDirectory) { d.gateSubject = "sub-bob" },
			signinsvc.ReasonGateSubject,
		},
		{"the gate checks two persons", func(d *fakeDirectory) { d.gateTwice = true }, signinsvc.ReasonGateSubject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newPasswordHarness(t, nil)
			tc.change(h.dir)

			_, err := h.signIn(t, "alice", "right")
			if got := refusalReason(t, err); got != tc.want {
				t.Fatalf("sign-in = %q, want %q", got, tc.want)
			}

			if len(h.w.starts) != 0 {
				t.Fatalf("a session started: %+v", h.w.starts)
			}
		})
	}
}

// TestPasswordLockedPersonWithAMethodThatIgnoresTheGate locks a person, and
// then signs in with the right password through a method that binds also when
// the gate refuses, and returns an Assertion. The service refuses the sign-in
// because the gate refused, whatever the method returned.
func TestPasswordLockedPersonWithAMethodThatIgnoresTheGate(t *testing.T) {
	t.Parallel()

	h := newPasswordHarness(t, nil)

	h.failTimes(t, "alice", 4)
	h.failTimes(t, "ALICE@corp", 3)
	h.failTimes(t, `corp\alice`, 3)

	h.dir.mu.Lock()
	h.dir.ignoreGate = true
	h.dir.mu.Unlock()

	_, err := h.signIn(t, "alice.smith", "right")
	if got := refusalReason(t, err); got != signinsvc.ReasonSubjectLocked {
		t.Fatalf("sign-in of a locked person = %q, want %q", got, signinsvc.ReasonSubjectLocked)
	}

	if !errors.Is(err, session.ErrMethodRejected) {
		t.Fatalf("err = %v, want the class of a wrong password", err)
	}

	if len(h.w.starts) != 0 {
		t.Fatalf("a session started for a locked person: %+v", h.w.starts)
	}
}

// TestPasswordUnknownUserWritesTheCountsOfAKnownPerson proves that a sign-in
// whose search finds no person adds as many failures to the store as a wrong
// password of a known person, so the cost in the store does not tell that a
// user exists. The count of the unknown person never locks: many attempts of
// unknown users never refuse a known person.
func TestPasswordUnknownUserWritesTheCountsOfAKnownPerson(t *testing.T) {
	t.Parallel()

	h := newPasswordHarness(t, nil)

	before := h.locks.added()
	h.failTimes(t, "bob", 1)
	known := h.locks.added() - before

	before = h.locks.added()
	h.failTimes(t, "nobody", 1)
	unknown := h.locks.added() - before

	if known != 4 || unknown != known {
		t.Fatalf("failures added: known person %d, unknown user %d; want 4 each", known, unknown)
	}

	for i := range 2 * lockLimit {
		if _, err := h.signIn(t, fmt.Sprintf("nobody-%d", i), "wrong"); err == nil {
			t.Fatalf("unknown user %d signed in", i)
		}
	}

	if _, err := h.signIn(t, "alice", "right"); err != nil {
		t.Fatalf("sign-in of alice after the attempts of unknown users = %v, want a session", err)
	}
}

// TestPasswordRequestOutsideTheBoundaryNeverReachesTheMethod sends input that
// no person can type, a provider of another kind, a method of another kind,
// and a service with no lockout store. None reaches the method.
func TestPasswordRequestOutsideTheBoundaryNeverReachesTheMethod(t *testing.T) {
	t.Parallel()

	oidc := ldapProvider()
	oidc.Kind = session.MethodOIDC

	cases := []struct {
		name     string
		userName string
		password string
		provider signinsvc.Provider
		kind     session.MethodKind
		want     string
	}{
		{"an empty password", "alice", "", ldapProvider(), session.MethodLDAP, signinsvc.ReasonInput},
		{"an empty user name", "", "right", ldapProvider(), session.MethodLDAP, signinsvc.ReasonInput},
		{
			"a user name over 256 bytes", strings.Repeat("a", session.MaxUserName+1), "right", ldapProvider(),
			session.MethodLDAP, signinsvc.ReasonInput,
		},
		{"a control character", "alice\x00", "right", ldapProvider(), session.MethodLDAP, signinsvc.ReasonInput},
		{"bytes that are not UTF-8", "alice\xff", "right", ldapProvider(), session.MethodLDAP, signinsvc.ReasonInput},
		{"a provider of another kind", "alice", "right", oidc, session.MethodLDAP, signinsvc.ReasonProviderKind},
		{"the local provider", "alice", "right", signinsvc.Provider{
			ID: session.LocalProviderID, Kind: session.MethodLDAP, AutoCreate: false, Stored: false,
		}, session.MethodLDAP, signinsvc.ReasonProviderKind},
		{
			"a method of another kind", "alice", "right", ldapProvider(), session.MethodSAML,
			signinsvc.ReasonPasswordMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newPasswordHarness(t, nil)
			h.dir.kind = tc.kind

			_, err := h.svc.CompletePassword(t.Context(), &signinsvc.PasswordRequest{
				Entry: callerEntry(), Provider: tc.provider, Method: h.dir, UserName: tc.userName,
				Password: session.NewPassword(tc.password),
			})
			if got := refusalReason(t, err); got != tc.want {
				t.Fatalf("sign-in = %q, want %q", got, tc.want)
			}

			if v := h.dir.counts().verifies; v != 0 {
				t.Fatalf("the method was called %d times, want 0", v)
			}
		})
	}
}

// TestPasswordWithoutCountsFailsClosed proves that a service with no lockout
// store, a key that cannot be read, or a short key refuses each sign-in before
// the method, so that no guess runs without a limit.
func TestPasswordWithoutCountsFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("no lockout store", checkNoLockoutStore)

	for _, tc := range []struct {
		name string
		keys keyReader
	}{
		{"a key that cannot be read", keyReader{key: nil, err: errDriver}},
		{"a short key", keyReader{key: make([]byte, 16), err: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newPasswordHarness(t, tc.keys)

			_, err := h.signIn(t, "alice", "right")
			if got := refusalReason(t, err); got != signinsvc.ReasonCountFailed {
				t.Fatalf("sign-in = %q, want %q", got, signinsvc.ReasonCountFailed)
			}

			if !errors.Is(err, session.ErrMethodUnavailable) {
				t.Fatalf("err = %v, want ErrMethodUnavailable", err)
			}

			if v := h.dir.counts().verifies; v != 0 {
				t.Fatalf("the method was called %d times, want 0", v)
			}
		})
	}
}

// TestPasswordRefusalHoldsNoPassword proves that no audit entry and no log
// record of a refused or a good sign-in holds the password.
func TestPasswordRefusalHoldsNoPassword(t *testing.T) {
	t.Parallel()

	const secret = "s3cret-Password-that-must-not-leak"

	h := newPasswordHarness(t, nil)
	h.dir.users["carol"] = fakeUser{subject: "sub-carol", password: secret}

	for _, try := range []struct{ name, password string }{
		{"carol", secret + "x"}, {"ghost", secret}, {"carol", secret},
	} {
		// The result does not matter: the case reads the records of each try.
		if _, err := h.signIn(t, try.name, try.password); err != nil && !signinsvc.IsRefusal(err) {
			t.Fatalf("sign-in of %s = %v, want a session or a refusal", try.name, err)
		}
	}

	for i := range h.w.entries {
		if strings.Contains(fmt.Sprintf("%+v", h.w.entries[i]), secret) {
			t.Fatalf("audit entry %d holds the password: %+v", i, h.w.entries[i])
		}
	}

	if strings.Contains(h.w.logs.text(), secret) {
		t.Fatalf("a log record holds the password: %s", h.w.logs.text())
	}
}

// checkNoLockoutStore proves that a service with no lockout store refuses a
// sign-in before the method.
func checkNoLockoutStore(t *testing.T) {
	t.Parallel()

	w := newWorld()

	svc, err := signinsvc.New(signinsvc.Config{
		Principals: w, Disabler: w, Generations: w, Sessions: w, Audit: w, TempAdmins: w, Groups: nil,
		Removals: nil, States: nil, Locks: nil, NameKeys: nil, Logger: slog.New(w.logs), OnAuditFailure: nil,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	dir := newDirectory()

	_, err = svc.CompletePassword(t.Context(), &signinsvc.PasswordRequest{
		Entry: callerEntry(), Provider: ldapProvider(), Method: dir, UserName: "alice",
		Password: session.NewPassword("right"),
	})
	if got := refusalReason(t, err); got != signinsvc.ReasonLockoutOff {
		t.Fatalf("sign-in = %q, want %q", got, signinsvc.ReasonLockoutOff)
	}

	if v := dir.counts().verifies; v != 0 {
		t.Fatalf("the method was called %d times, want 0", v)
	}
}
