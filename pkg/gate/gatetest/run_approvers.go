package gatetest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// approverWriter puts one object of one kind with the users as its approvers.
type approverWriter struct {
	name string
	put  func(e *env, users []string) error
}

// approverWriters returns a writer for each object that names approvers by
// subject: the approval rule, the ownership rule and the approval default. A
// writer for the approval default is left out when the store has no settings
// port.
func approverWriters(e *env) []approverWriter {
	scope := e.fx.acme
	approvers := func(users []string) gate.Approvers {
		return gate.Approvers{Groups: nil, GroupIDs: nil, Users: users, UnitOwners: false}
	}

	out := make([]approverWriter, 0, 3)
	out = append(out,
		approverWriter{"approval rule", func(e *env, users []string) error {
			r := sample[gate.ApprovalRule](e.t, scope, "a", "one")
			r.Approvers = approvers(users)
			_, err := e.h.Store.ApprovalRules().Put(e.t.Context(), r, 0, e.fence(scope), e.entry(scope))

			return err
		}},
		approverWriter{"ownership rule", func(e *env, users []string) error {
			r := sample[gate.OwnershipRule](e.t, scope, "a", "one")
			r.Approvers = approvers(users)
			_, err := e.h.Store.OwnershipRules().Put(e.t.Context(), r, 0, e.fence(scope), e.entry(scope))

			return err
		}},
	)

	if e.h.Store.Settings() == nil {
		return out
	}

	return append(out, approverWriter{"approval default", func(e *env, users []string) error {
		d := approvalDefault(scope, 1)
		d.Approvers = approvers(users)
		_, err := e.h.Store.Settings().PutApprovalDefault(e.t.Context(), d, 0, e.fence(scope), e.entry(scope))

		return err
	}})
}

// RunApprovers executes the cases of the users of the approvers: a write of an
// approval rule, an ownership rule or an approval default refuses the value
// gate.NoApprover, each subject that starts with "!", and, on a production
// installation, each dev subject, with gate.ErrInvalid and no change. A dev
// installation takes a dev subject. newProduction gives a store of a
// production installation. newDev gives a store of a dev installation, or is
// nil when the adapter cannot make one, and then the dev case is left out.
func RunApprovers(t *testing.T, newProduction, newDev Factory) {
	t.Helper()

	t.Run("production refuses", func(t *testing.T) {
		t.Parallel()
		runProductionApprovers(t, newProduction)
	})

	if newDev == nil {
		return
	}

	t.Run("dev installation", func(t *testing.T) {
		t.Parallel()
		runDevApprovers(t, newDev)
	})
}

func runProductionApprovers(t *testing.T, newProduction Factory) {
	t.Helper()

	refused := []struct{ name, user string }{
		{"NoApprover", gate.NoApprover},
		{"exclamation mark", "!x"},
		{"dev subject", "dev:admin"},
		{"dev subject in other case", "DEV:x"},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, newProduction(t))

			for _, w := range approverWriters(e) {
				wantErr(t, "put "+w.name+" with "+tc.user, w.put(e, []string{"alice", tc.user}), gate.ErrInvalid)
				e.wantEntries("acme", 0)
			}
		})
	}

	t.Run("a person is accepted", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t, newProduction(t))

		for _, w := range approverWriters(e) {
			mustNil(t, "put "+w.name+" with a person", w.put(e, []string{"alice"}))
		}
	})
}

func runDevApprovers(t *testing.T, newDev Factory) {
	t.Helper()

	for _, user := range []string{gate.NoApprover, "!x"} {
		t.Run(user+" is refused", func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, newDev(t))

			for _, w := range approverWriters(e) {
				wantErr(t, "put "+w.name+" with "+user, w.put(e, []string{user}), gate.ErrInvalid)
			}
		})
	}

	t.Run("dev subject is accepted", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t, newDev(t))

		for _, w := range approverWriters(e) {
			mustNil(t, "put "+w.name+" with dev:admin", w.put(e, []string{"dev:admin"}))
		}
	})
}
