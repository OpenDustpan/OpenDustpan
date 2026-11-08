package principal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errRefused is what the fake guard answers for a refused record.
var errRefused = errors.New("refused")

// fakeGuard refuses the records in refuse and counts its checks.
type fakeGuard struct {
	refuse map[string]bool
	checks *int
}

func (g fakeGuard) CheckGroupGrants(_ context.Context, _ identity.Principal, groupID string) error {
	*g.checks++

	if g.refuse[groupID] {
		return errRefused
	}

	return nil
}

func (g fakeGuard) CheckPrincipalGrants(_ context.Context, _ identity.Principal, principalID string) error {
	*g.checks++

	if g.refuse[principalID] {
		return errRefused
	}

	return nil
}

// TestGuardChecksEveryChangeOfWhoHoldsGrants proves that a membership change,
// a delete and an enable ask the Guard first, and that a refusal changes
// nothing.
func TestGuardChecksEveryChangeOfWhoHoldsGrants(t *testing.T) {
	t.Parallel()

	checks := 0
	guard := fakeGuard{refuse: map[string]bool{}, checks: &checks}

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      openStore(t),
			Guard:      guard,
			Namespaces: nil,
			Tokens:     nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	ctx := t.Context()
	e := testEntry()
	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "tester",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
		JobUnit:        identity.Object{},
	}

	p, err := svc.CreatePrincipal(
		ctx,
		e,
		principal.Input{Subject: "alice", DisplayName: "", Kind: principal.KindPerson},
	)
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	g, err := svc.CreateGroup(ctx, e, principal.GroupInput{Name: "ops", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	guard.refuse[g.ID] = true
	guard.refuse[p.ID] = true

	if _, err := svc.AddMember(ctx, e, caller, g.ID, p.ID); !errors.Is(err, errRefused) {
		t.Fatalf("add member = %v, want the refusal of the guard", err)
	}

	if err := svc.RemoveMember(ctx, e, caller, g.ID, p.ID); !errors.Is(err, errRefused) {
		t.Fatalf("remove member = %v, want the refusal of the guard", err)
	}

	if err := svc.DeleteGroup(ctx, e, caller, g.ID); !errors.Is(err, errRefused) {
		t.Fatalf("delete group = %v, want the refusal of the guard", err)
	}

	if err := svc.EnablePrincipal(ctx, e, caller, p.ID); !errors.Is(err, errRefused) {
		t.Fatalf("enable principal = %v, want the refusal of the guard", err)
	}

	if err := svc.DeletePrincipal(ctx, e, caller, p.ID); !errors.Is(err, errRefused) {
		t.Fatalf("delete principal = %v, want the refusal of the guard", err)
	}

	if _, err := svc.GetGroup(ctx, g.ID); err != nil {
		t.Fatalf("group after the refused delete: %v", err)
	}

	if checks != 5 {
		t.Fatalf("guard checks = %d, want 5", checks)
	}

	delete(guard.refuse, g.ID)

	if _, err := svc.AddMember(ctx, e, caller, g.ID, p.ID); err != nil {
		t.Fatalf("add member that the guard allows: %v", err)
	}
}

// TestGuardChecksDisable proves that a disable asks the Guard first, that a
// refusal changes nothing, and that an allowed disable goes through.
func TestGuardChecksDisable(t *testing.T) {
	t.Parallel()

	checks := 0
	guard := fakeGuard{refuse: map[string]bool{}, checks: &checks}

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      openStore(t),
			Guard:      guard,
			Namespaces: nil,
			Tokens:     nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	ctx := t.Context()
	e := testEntry()

	var caller identity.Principal

	in := principal.Input{Subject: "alice", DisplayName: "", Kind: principal.KindPerson}

	p, err := svc.CreatePrincipal(ctx, e, in)
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	guard.refuse[p.ID] = true

	if disableErr := svc.DisablePrincipal(ctx, e, caller, p.ID); !errors.Is(disableErr, errRefused) {
		t.Fatalf("disable principal = %v, want the refusal of the guard", disableErr)
	}

	got, err := svc.GetPrincipal(ctx, p.ID)
	if err != nil || !got.DisabledAt.IsZero() {
		t.Fatalf("principal after the refused disable = %+v, %v, want enabled", got, err)
	}

	delete(guard.refuse, p.ID)

	if disableErr := svc.DisablePrincipal(ctx, e, caller, p.ID); disableErr != nil {
		t.Fatalf("disable principal that the guard allows: %v", disableErr)
	}

	if checks != 2 {
		t.Fatalf("guard checks = %d, want 2", checks)
	}
}
