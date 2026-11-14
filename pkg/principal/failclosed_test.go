package principal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// callerOf returns a caller that is an administrator or is not.
func callerOf(admin bool) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "tester", DisplayName: "", Admin: admin, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: "", Credential: "", JobID: "",
	}
}

// TestNewServiceNeedsAGuard proves that a Config without a Guard is refused.
func TestNewServiceNeedsAGuard(t *testing.T) {
	t.Parallel()

	_, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: openStore(t), Guard: nil, Namespaces: nil, Tokens: nil, GroupReach: nil,
		Now: nil, NewID: nil,
	})
	if !errors.Is(err, principal.ErrMissingDependency) {
		t.Fatalf("NewService without a guard = %v, want ErrMissingDependency", err)
	}
}

// guardOp is one change that asks the Guard.
type guardOp struct {
	name string
	// disabled tells that the principal is disabled before the call.
	disabled bool
	// member tells that the principal is a member of the group before the call.
	member bool
	run    func(ctx context.Context, svc *principal.Service, caller identity.Principal, pid, gid string) error
}

func guardOps() []guardOp {
	e := testEntry()

	return []guardOp{
		{
			"enable",
			true,
			false,
			func(ctx context.Context, s *principal.Service, c identity.Principal, pid, _ string) error {
				return s.EnablePrincipal(ctx, e, c, pid)
			},
		},
		{
			"disable",
			false,
			false,
			func(ctx context.Context, s *principal.Service, c identity.Principal, pid, _ string) error {
				return s.DisablePrincipal(ctx, e, c, pid)
			},
		},
		{
			"delete principal",
			false,
			false,
			func(ctx context.Context, s *principal.Service, c identity.Principal, pid, _ string) error {
				return s.DeletePrincipal(ctx, e, c, pid)
			},
		},
		{
			"delete group",
			false,
			false,
			func(ctx context.Context, s *principal.Service, c identity.Principal, _, gid string) error {
				return s.DeleteGroup(ctx, e, c, gid)
			},
		},
		{
			"add member",
			false,
			false,
			func(ctx context.Context, s *principal.Service, c identity.Principal, pid, gid string) error {
				_, err := s.AddMember(ctx, e, c, gid, pid)

				return err
			},
		},
		{
			"remove member",
			false,
			true,
			func(ctx context.Context, s *principal.Service, c identity.Principal, pid, gid string) error {
				return s.RemoveMember(ctx, e, c, gid, pid)
			},
		},
	}
}

// guardFixture is a service with the admin-only guard, with one principal
// and one group.
type guardFixture struct {
	svc      *principal.Service
	pid, gid string
}

// newGuardFixture returns a guardFixture.
func newGuardFixture(t *testing.T) guardFixture {
	t.Helper()

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: openStore(t), Guard: principal.AdminOnlyGuard{},
		Namespaces: nil, Tokens: nil, GroupReach: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	p, err := svc.CreatePrincipal(t.Context(), testEntry(),
		principal.Input{Subject: "alice", DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	g, err := svc.CreateGroup(t.Context(), testEntry(), principal.GroupInput{Name: "ops", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	return guardFixture{svc: svc, pid: p.ID, gid: g.ID}
}

// runGuardOp runs op as caller on a new fixture and returns its error.
func runGuardOp(t *testing.T, op guardOp, caller identity.Principal) error {
	t.Helper()

	fx := newGuardFixture(t)

	if op.disabled {
		if err := fx.svc.DisablePrincipal(t.Context(), testEntry(), callerOf(true), fx.pid); err != nil {
			t.Fatalf("prepare disable: %v", err)
		}
	}

	if op.member {
		if _, err := fx.svc.AddMember(t.Context(), testEntry(), callerOf(true), fx.gid, fx.pid); err != nil {
			t.Fatalf("prepare member: %v", err)
		}
	}

	return op.run(t.Context(), fx.svc, caller, fx.pid, fx.gid)
}

// TestAdminOnlyGuardRefusesANonAdministrator proves that a service with the
// admin-only guard refuses a caller who is not an administrator for every
// change that asks the Guard, and lets an administrator through.
func TestAdminOnlyGuardRefusesANonAdministrator(t *testing.T) {
	t.Parallel()

	for _, op := range guardOps() {
		t.Run(op.name+"/non-admin", func(t *testing.T) {
			t.Parallel()

			if err := runGuardOp(t, op, callerOf(false)); !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("%s by a non-administrator = %v, want identity.ErrDenied", op.name, err)
			}
		})

		t.Run(op.name+"/admin", func(t *testing.T) {
			t.Parallel()

			if err := runGuardOp(t, op, callerOf(true)); err != nil {
				t.Fatalf("%s by an administrator = %v, want success", op.name, err)
			}
		})
	}
}

// TestZeroServiceRefusesANonAdministrator proves the fallback in the check:
// a Service that NewService did not build has no Guard, and still refuses a
// non-administrator before it reads any store.
func TestZeroServiceRefusesANonAdministrator(t *testing.T) {
	t.Parallel()

	for _, op := range guardOps() {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			var svc principal.Service

			if err := op.run(t.Context(), &svc, callerOf(false), "p", "g"); !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("%s = %v, want identity.ErrDenied", op.name, err)
			}
		})
	}
}
