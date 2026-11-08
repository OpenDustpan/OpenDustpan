package principal_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errRefused is what the fake guard answers for a refused record.
var errRefused = errors.New("refused")

// groupCheck is one check of the guard: the group, the name that a rename
// gives it and the change.
type groupCheck struct {
	groupID string
	newName string
	change  principal.GroupChange
}

// fakeGuard refuses the records in refuse, counts its checks and records each
// group check.
type fakeGuard struct {
	refuse  map[string]bool
	checks  *int
	changes *[]groupCheck
}

func (g fakeGuard) CheckGroupGrants(
	_ context.Context, _ identity.Principal, groupID, newName string, change principal.GroupChange,
) error {
	*g.checks++
	*g.changes = append(*g.changes, groupCheck{groupID: groupID, newName: newName, change: change})

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

// fakeReach allows every membership add. The Guard tests wire it, so that a
// Service without the access service lets the Guard decide.
type fakeReach struct{}

func (fakeReach) CheckGroupReach(context.Context, identity.Principal, string) error { return nil }

// TestGuardChecksEveryChangeOfWhoHoldsGrants proves that a membership change,
// a rename, a delete and an enable ask the Guard first, and that a refusal
// changes nothing.
func TestGuardChecksEveryChangeOfWhoHoldsGrants(t *testing.T) {
	t.Parallel()

	checks := 0
	changes := []groupCheck{}
	guard := fakeGuard{refuse: map[string]bool{}, checks: &checks, changes: &changes}

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      openStore(t),
			Guard:      guard,
			GroupReach: fakeReach{},
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

	_, addErr := svc.AddMember(ctx, e, caller, g.ID, p.ID)
	refused(t, addErr, "add member")

	removeErr := svc.RemoveMember(ctx, e, caller, g.ID, p.ID)
	refused(t, removeErr, "remove member")

	renamed := "ops-two"

	_, renameErr := svc.UpdateGroup(ctx, e, caller, g.ID, principal.GroupUpdate{
		Name: &renamed, Description: nil, ClosedToIdentityProvider: nil,
	})
	refused(t, renameErr, "rename group")

	deleteErr := svc.DeleteGroup(ctx, e, caller, g.ID)
	refused(t, deleteErr, "delete group")

	enableErr := svc.EnablePrincipal(ctx, e, caller, p.ID)
	refused(t, enableErr, "enable principal")

	deletePrincipalErr := svc.DeletePrincipal(ctx, e, caller, p.ID)
	refused(t, deletePrincipalErr, "delete principal")

	got, err := svc.GetGroup(ctx, g.ID)
	if err != nil || got.Name != "ops" {
		t.Fatalf("group after the refused rename and delete = %+v, %v, want the name ops", got, err)
	}

	if checks != 6 {
		t.Fatalf("guard checks = %d, want 6", checks)
	}

	checkGroupChanges(t, changes, []groupCheck{
		{groupID: g.ID, newName: "", change: principal.GroupChangeAddMember},
		{groupID: g.ID, newName: "", change: principal.GroupChangeRemoveMember},
		{groupID: g.ID, newName: renamed, change: principal.GroupChangeRename},
		{groupID: g.ID, newName: "", change: principal.GroupChangeDelete},
	})

	delete(guard.refuse, g.ID)

	_, addAllowedErr := svc.AddMember(ctx, e, caller, g.ID, p.ID)
	if addAllowedErr != nil {
		t.Fatalf("add member that the guard allows: %v", addAllowedErr)
	}

	g, err = svc.UpdateGroup(ctx, e, caller, g.ID, principal.GroupUpdate{
		Name: &renamed, Description: nil, ClosedToIdentityProvider: nil,
	})
	if err != nil || g.Name != renamed {
		t.Fatalf("rename group that the guard allows = %+v, %v, want the name %s", g, err, renamed)
	}
}

// refused fails the test when err is not the refusal of the guard.
func refused(t *testing.T, err error, what string) {
	t.Helper()

	if !errors.Is(err, errRefused) {
		t.Fatalf("%s = %v, want the refusal of the guard", what, err)
	}
}

// checkGroupChanges fails the test when the guard did not see the changes.
func checkGroupChanges(t *testing.T, got, want []groupCheck) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("group checks = %+v, want %+v", got, want)
	}
}

// TestGuardChecksDisable proves that a disable asks the Guard first, that a
// refusal changes nothing, and that an allowed disable goes through.
func TestGuardChecksDisable(t *testing.T) {
	t.Parallel()

	checks := 0
	changes := []groupCheck{}
	guard := fakeGuard{refuse: map[string]bool{}, checks: &checks, changes: &changes}

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      openStore(t),
			Guard:      guard,
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
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
