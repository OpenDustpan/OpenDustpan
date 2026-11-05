package localusersvc_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// errStopped is the error of a stopped store.
var errStopped = errors.New("the store does not answer")

// stoppable is a store whose reads of a credential fail after stop.
type stoppable struct {
	localusersvc.Stores

	stopped *atomic.Bool
}

func (s stoppable) UserByName(ctx context.Context, name string) (localuser.Credential, error) {
	if s.stopped.Load() {
		return localuser.Credential{}, errStopped
	}

	return s.Stores.UserByName(ctx, name)
}

// methodService returns a service over the store of f whose reads of a
// credential fail after stopped is set.
func (f *fixture) methodService(t *testing.T, stopped *atomic.Bool) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Log:   nil,
		Store: stoppable{Stores: f.store, stopped: stopped}, Principals: f.principals, Hasher: f.hasher,
		Blocklist: blocklist.Embedded(), Audit: f.store, Locks: f.store, Keys: f.store, Settings: f.settings,
		SSO: func(context.Context) (bool, error) { return false, nil }, Reach: f.storeReach(),
		Kind: installation.KindProduction, Now: nil, Subjects: f.store,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// passwordFixture returns the fixture of the suite over a new fixture.
func passwordFixture(t *testing.T) sessiontest.PasswordFixture {
	t.Helper()

	f := newFixture(t)
	stopped := &atomic.Bool{}
	svc := f.methodService(t, stopped)
	f.svc = svc

	return sessiontest.PasswordFixture{
		Method: svc.PasswordMethod(base("")),
		AddUser: func(t *testing.T, p sessiontest.TestPerson, password string) string {
			t.Helper()

			return f.mustCreate(t, p.UserName, password).User.ID
		},
		UserNameForms: func(name string) []string {
			return []string{strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]}
		},
		Stop: func(*testing.T) { stopped.Store(true) },
		Disable: func(*testing.T) {
			f.settings.values[localuser.SettingEnabled] = "false"
		},
		Now:        svc.Now,
		ProviderID: session.LocalProviderID, Groups: false, ReportsDisabled: false,
	}
}

// TestPasswordMethodConformance runs the suite of session.PasswordMethod on
// the password sign-in of the local users.
func TestPasswordMethodConformance(t *testing.T) {
	t.Parallel()

	sessiontest.RunPasswordMethod(t, passwordFixture)
}

// TestSignInReturnsTheAssertion checks the Assertion of a good password
// sign-in: one factor, the password, a known time of the check, the
// principal, and the ID of the local user as the subject. The sign-in
// service accepts it.
func TestSignInReturnsTheAssertion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	before := f.svc.Now()

	got, err := f.svc.SignIn(t.Context(), base(""), "Alice", goodPassword)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	a := got.Assertion
	if a.Method != session.MethodLocalPassword || a.ProviderID != session.LocalProviderID ||
		a.PrincipalID != c.Principal.ID || a.Subject != c.User.ID || a.UserName != "alice" {
		t.Fatalf("Assertion = %+v, want the local user %s of the principal %s", a, c.User.ID, c.Principal.ID)
	}

	checkLocalAssertion(t, a, before, f.svc.Now())
}

// checkLocalAssertion fails unless a holds one factor, the password, a known
// time of the check between before and after, and no groups, and passes the
// check of the sign-in service.
func checkLocalAssertion(t *testing.T, a session.Assertion, before, after time.Time) {
	t.Helper()

	if len(a.Factors) != 1 || a.Factors[0] != session.FactorPassword {
		t.Fatalf("factors = %v, want one factor, the password", a.Factors)
	}

	if !a.AuthTimeKnown || a.AuthTime.Before(before) || a.AuthTime.After(after) {
		t.Fatalf("auth time = %v (known %v), want the time of the check", a.AuthTime, a.AuthTimeKnown)
	}

	if a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want none", a.Groups, a.GroupsPresent)
	}

	if err := session.CheckAssertion(a, after); err != nil {
		t.Fatalf("CheckAssertion() = %v", err)
	}
}

// stopGate is a bind gate that stops each check.
type stopGate struct{}

// errLocked is the error of stopGate.
var errLocked = errors.New("the person is locked")

func (stopGate) BeforeBind(context.Context, string) error { return errLocked }

// TestPasswordMethodGateStopWritesARefusal checks that a gate that stops the
// check of a known user writes the refusal with its own reason, and that the
// stop never signs the person in, also with the good password.
func TestPasswordMethodGateStopWritesARefusal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mustCreate(t, "alice", goodPassword)

	a, err := f.svc.PasswordMethod(base("")).Verify(t.Context(), session.Credentials{
		ProviderID: session.LocalProviderID, UserName: "alice", Password: session.NewPassword(goodPassword),
	}, stopGate{})
	if !errors.Is(err, errLocked) || a.PrincipalID != "" {
		t.Fatalf("Verify() = %+v, %v, want the error of the gate", a, err)
	}

	f.checkFailureEntries(t, []string{localusersvc.ReasonBindGate})
}

// TestPasswordMethodErrorsOfTheService checks the error of package session
// for each error of the service, and that an error of no known class fails
// closed.
func TestPasswordMethodErrorsOfTheService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		setup func(f *fixture)
		want  error
		name  string
	}{
		{
			func(f *fixture) { f.settings.values[localuser.SettingEnabled] = "false" }, session.ErrProviderDisabled,
			"password sign-in off",
		},
		{func(f *fixture) { f.settings.err = errStopped }, session.ErrMethodUnavailable, "settings fail"},
		{func(*fixture) {}, session.ErrMethodRejected, "wrong password"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.mustCreate(t, "alice", goodPassword)
			tc.setup(f)

			a, err := f.svc.PasswordMethod(base("")).Verify(t.Context(), session.Credentials{
				ProviderID: session.LocalProviderID, UserName: "alice", Password: session.NewPassword(otherPassword),
			}, stopNever{})
			if !errors.Is(err, tc.want) || a.Subject != "" {
				t.Fatalf("Verify() = %+v, %v, want %v", a, err, tc.want)
			}
		})
	}
}

// stopNever is a bind gate that lets each check through.
type stopNever struct{}

func (stopNever) BeforeBind(context.Context, string) error { return nil }
