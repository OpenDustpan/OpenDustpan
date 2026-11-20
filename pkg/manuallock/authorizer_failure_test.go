package manuallock_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// errAuthorizerDown is a plain error: it does not wrap identity.ErrDenied.
var errAuthorizerDown = errors.New("authorizer backend down")

// funcAuthorizer answers every check with the error of its function.
type funcAuthorizer func() error

func (f funcAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return f()
}

type authzCase struct {
	name       string
	call       string
	target     manuallock.Target
	authz      funcAuthorizer
	wantDenied bool
}

// run makes the call of the case on svc.
func (tc authzCase) run(svc *manuallock.Service) error {
	if tc.call == "lock" {
		_, err := svc.Lock(context.Background(), person("alice"), tc.target, "freeze", entry())

		return err
	}

	return svc.Unlock(context.Background(), person("alice"), tc.target, entry())
}

// authzCases is each call on each kind of target with each kind of answer.
func authzCases() []authzCase {
	targets := map[string]manuallock.Target{
		"unit":       manuallock.UnitTarget(webUnit()),
		"collection": manuallock.CollectionTarget(paymentsRef()),
		"namespace":  acmeTarget(),
	}
	answers := map[string]funcAuthorizer{
		"failing authorizer": func() error { return errAuthorizerDown },
		"denying authorizer": func() error { return identity.ErrDenied },
	}

	cases := make([]authzCase, 0, len(targets)*len(answers)*2)

	for _, call := range []string{"lock", "unlock"} {
		for tn, tg := range targets {
			for an, authz := range answers {
				cases = append(cases, authzCase{
					name: call + "/" + tn + "/" + an, call: call, target: tg, authz: authz,
					wantDenied: an == "denying authorizer",
				})
			}
		}
	}

	return cases
}

// TestAuthorizerErrorsAreTold checks that an Authorizer that fails with a plain
// error gives ErrAuthorizerFailed and not ErrDenied, that an Authorizer that
// denies gives ErrDenied and not ErrAuthorizerFailed, and that the store is not
// called in both cases.
func TestAuthorizerErrorsAreTold(t *testing.T) {
	t.Parallel()

	for _, tc := range authzCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			svc := newServiceWith(t, store, tc.authz)

			err := tc.run(svc)

			if err == nil {
				t.Fatal("err = nil, want a refusal")
			}

			tc.check(t, err, store)
		})
	}
}

// check compares the answer of the call with the case.
func (tc authzCase) check(t *testing.T, err error, store *recordingStore) {
	t.Helper()

	if got := errors.Is(err, identity.ErrDenied); got != tc.wantDenied {
		t.Errorf("errors.Is(err, ErrDenied) = %v, want %v (err = %v)", got, tc.wantDenied, err)
	}

	if got := errors.Is(err, manuallock.ErrAuthorizerFailed); got == tc.wantDenied {
		t.Errorf("errors.Is(err, ErrAuthorizerFailed) = %v, want %v (err = %v)", got, !tc.wantDenied, err)
	}

	if len(store.put) != 0 || len(store.removed) != 0 || store.forced != 0 {
		t.Errorf("store called: put=%d removed=%d forced=%d, want none",
			len(store.put), len(store.removed), store.forced)
	}
}
