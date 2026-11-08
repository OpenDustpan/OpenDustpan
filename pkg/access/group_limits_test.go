package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errLimitsRead is the error of a read of the access limits that fails.
var errLimitsRead = errors.New("read failed")

// errLimitParse is the error of an access limit that does not parse.
var errLimitParse = errors.New("policy refused")

// errGrantsRead is the error of a read of the grants that fails.
var errGrantsRead = errors.New("grants read failed")

// limitKey names one answer of fakeLimits: the group, the name that a
// rename gives it and the change.
type limitKey struct {
	group   string
	newName string
	change  principal.GroupChange
}

// fakeLimits is an access.LimitGroups that answers from a table, or with an
// error.
type fakeLimits struct {
	lifted map[limitKey]bool
	kind   access.LimitKind
	err    error
}

func (f fakeLimits) LimitLiftedBy(
	_ context.Context, groupID, newName string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	if f.err != nil {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, f.err
	}

	if !f.lifted[limitKey{group: groupID, newName: newName, change: change}] {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
	}

	return access.LimitRef{ID: "lim-1", Scope: "namespace/acme", Kind: f.kind}, true, nil
}

// failingGrantsStore is the fixture store with a read of the grants that
// fails. It keeps every other method of the store.
type failingGrantsStore struct {
	access.Store
}

func (s *failingGrantsStore) GrantsOn(
	_ context.Context, _ []access.Subject, _ []identity.Object,
) ([]access.Grant, error) {
	return nil, errGrantsRead
}

// serviceWithLimits returns a service over the fixture store whose guard
// reads the access limits through limits, as the server wires it. The clock
// is fixed, so the test does not read the wall clock.
func serviceWithLimits(tb testing.TB, f *fixture, limits access.LimitGroups) *access.Service {
	tb.Helper()

	svc, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: f.store, Namespaces: f.store, Authorizer: f.authz,
		Now: func() time.Time { return time.UnixMilli(1_760_000_000_000) }, NewID: nil,
		BootstrapAdmins: []string{"admin"}, LimitGroups: limits,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
	})
	if err != nil {
		tb.Fatalf("new service: %v", err)
	}

	return svc
}

// TestCheckGroupGrantsReadsTheAccessLimits proves the rule: a change that
// can lift an access limit that names the group needs a site administrator,
// also when the caller holds every action that the grants of the group
// give. A change that can only add a forbid keeps the grants check.
func TestCheckGroupGrantsReadsTheAccessLimits(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")

	// The group holds a viewer grant on acme, which the account
	// administrator holds too, so the grants check lets the change pass.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	lifted := func(changes ...principal.GroupChange) fakeLimits {
		table := map[limitKey]bool{}
		for _, change := range changes {
			table[limitKey{group: readers.ID, newName: "", change: change}] = true
		}

		return fakeLimits{lifted: table, kind: access.LimitKindAccessLimit, err: nil}
	}

	cases := []struct {
		name   string
		caller identity.Principal
		limits access.LimitGroups
		change principal.GroupChange
		// newName is the name that a rename gives the group.
		newName string
		// text is the change that a refusal names.
		text string
		want error
	}{
		{
			name:    "an add to a group of an unless clause is refused",
			caller:  acct,
			limits:  lifted(principal.GroupChangeAddMember),
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "add a member",
			want:    access.ErrEscalation,
		},
		{
			name:    "a remove from a group of a when clause is refused",
			caller:  acct,
			limits:  lifted(principal.GroupChangeRemoveMember),
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			text:    "remove a member",
			want:    access.ErrEscalation,
		},
		{
			name:    "a remove from a group of an unless clause keeps the grants check",
			caller:  acct,
			limits:  lifted(principal.GroupChangeAddMember),
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "an add to a group of a when clause keeps the grants check",
			caller:  acct,
			limits:  lifted(principal.GroupChangeRemoveMember),
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "the delete of a group of a when clause is refused",
			caller:  acct,
			limits:  lifted(principal.GroupChangeDelete),
			change:  principal.GroupChangeDelete,
			newName: "",
			text:    "delete the group",
			want:    access.ErrEscalation,
		},
		{
			name:   "a rename to the name of a group of an unless clause is refused",
			caller: acct,
			limits: fakeLimits{
				lifted: map[limitKey]bool{
					{group: readers.ID, newName: "auditors", change: principal.GroupChangeRename}: true,
				},
				kind: access.LimitKindAccessLimit,
				err:  nil,
			},
			change:  principal.GroupChangeRename,
			newName: "auditors",
			text:    "rename the group",
			want:    access.ErrEscalation,
		},
		{
			name:   "a rename that no limit names keeps the grants check",
			caller: acct,
			limits: fakeLimits{
				lifted: map[limitKey]bool{
					{group: readers.ID, newName: "auditors", change: principal.GroupChangeRename}: true,
				},
				kind: access.LimitKindAccessLimit,
				err:  nil,
			},
			change:  principal.GroupChangeRename,
			newName: "readers-two",
			text:    "",
			want:    nil,
		},
		{
			name:    "an administrator adds a member of a group of an unless clause",
			caller:  admin(),
			limits:  lifted(principal.GroupChangeAddMember),
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "an administrator removes a member of a group of a when clause",
			caller:  admin(),
			limits:  lifted(principal.GroupChangeRemoveMember),
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "an administrator deletes a group of a when clause",
			caller:  admin(),
			limits:  lifted(principal.GroupChangeDelete),
			change:  principal.GroupChangeDelete,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "a group that no limit names keeps the grants check",
			caller:  acct,
			limits:  fakeLimits{lifted: map[limitKey]bool{}, kind: access.LimitKindAccessLimit, err: nil},
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:   "a limit that names another group keeps the grants check",
			caller: acct,
			limits: fakeLimits{
				lifted: map[limitKey]bool{
					{group: "grp-other", newName: "", change: principal.GroupChangeAddMember}: true,
				},
				kind: access.LimitKindAccessLimit,
				err:  nil,
			},
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    nil,
		},
		{
			name:    "the read of the limits fails and refuses the change",
			caller:  acct,
			limits:  fakeLimits{lifted: nil, kind: access.LimitKindAccessLimit, err: errLimitsRead},
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    access.ErrEscalation,
		},
		{
			name:    "a limit that does not parse refuses the change",
			caller:  acct,
			limits:  fakeLimits{lifted: nil, kind: access.LimitKindAccessLimit, err: errLimitParse},
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    access.ErrEscalation,
		},
		{
			name:    "a service without the port keeps the grants check",
			caller:  acct,
			limits:  nil,
			change:  principal.GroupChangeAddMember,
			newName: "",
			text:    "",
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := serviceWithLimits(t, f, tc.limits)

			err := svc.CheckGroupGrants(ctx, tc.caller, readers.ID, tc.newName, tc.change)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckGroupGrants(readers, %q, %d) = %v, want %v", tc.newName, tc.change, err, tc.want)
			}

			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("the refusal = %q, want %q", err, tc.text)
			}
		})
	}
}

// TestCheckGroupGrantsNamesTheKindOfTheObject proves that the refusal of a
// change that can widen access through a gate or an approval rule carries the
// sentinel that the routes map to 403, names the kind and the ID of the
// object when the caller may read the gates of the namespace, and names
// neither the ID nor the scope when the caller may not.
func TestCheckGroupGrantsNamesTheKindOfTheObject(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")
	carol := f.person(t, "carol")

	// readers holds a viewer grant on acme, which the account
	// administrator holds too, so the grants check lets the change pass.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	// ops holds no grant, so the grants check lets carol pass.
	ops := f.group(t, "ops")

	cases := []struct {
		name   string
		caller identity.Principal
		group  access.Subject
		kind   access.LimitKind
		want   error
		// wants are the texts that the refusal must hold.
		wants []string
		// hides are the texts that the refusal must not hold.
		hides []string
	}{
		{
			name:   "a gate that the caller may read names its kind and ID",
			caller: acct, group: readers, kind: access.LimitKindGate, want: access.ErrEscalation,
			wants: []string{`a gate "lim-1" of namespace/acme`, readers.ID},
			hides: nil,
		},
		{
			name:   "an approval rule that the caller may read names its kind and ID",
			caller: acct, group: readers, kind: access.LimitKindApprovalRule, want: access.ErrEscalation,
			wants: []string{`an approval rule "lim-1" of namespace/acme`, readers.ID},
			hides: nil,
		},
		{
			name:   "a gate that the caller may not read names no ID and no scope",
			caller: carol, group: ops, kind: access.LimitKindGate, want: access.ErrEscalation,
			wants: []string{"a gate that you cannot read", ops.ID},
			hides: []string{"lim-1", "namespace/acme", "acme"},
		},
		{
			name:   "an approval rule that the caller may not read names no ID and no scope",
			caller: carol, group: ops, kind: access.LimitKindApprovalRule, want: access.ErrEscalation,
			wants: []string{"an approval rule that you cannot read", ops.ID},
			hides: []string{"lim-1", "namespace/acme", "acme"},
		},
		{
			name:   "a site administrator passes a gate",
			caller: admin(), group: readers, kind: access.LimitKindGate, want: nil,
			wants: nil, hides: nil,
		},
		{
			name:   "a site administrator passes an approval rule",
			caller: admin(), group: readers, kind: access.LimitKindApprovalRule, want: nil,
			wants: nil, hides: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limits := fakeLimits{
				lifted: map[limitKey]bool{
					{group: tc.group.ID, newName: "", change: principal.GroupChangeAddMember}: true,
				},
				kind: tc.kind,
				err:  nil,
			}
			svc := serviceWithLimits(t, f, limits)

			err := svc.CheckGroupGrants(ctx, tc.caller, tc.group.ID, "", principal.GroupChangeAddMember)
			checkKindRefusal(t, err, tc.want, tc.wants, tc.hides)
		})
	}
}

// checkKindRefusal checks the refusal of a change that can widen access
// through an object of a kind.
func checkKindRefusal(t *testing.T, err, want error, wants, hides []string) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("CheckGroupGrants() = %v, want %v", err, want)
	}

	if err == nil {
		return
	}

	for _, text := range wants {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("the refusal = %q, want %q", err, text)
		}
	}

	for _, text := range hides {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("the refusal = %q, want no %q", err, text)
		}
	}
}

// TestCheckGroupGrantsAllowsASiteAdministratorByTheDecision proves that the
// limit check lets a change pass for a caller whom the access decision makes
// a site administrator, and refuses a caller who holds less, though it holds
// every grant of the group.
func TestCheckGroupGrantsAllowsASiteAdministratorByTheDecision(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	// The group holds a viewer grant on acme, which each caller below
	// holds too, so only the limit check decides.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	site := f.person(t, "site")
	f.grant(t, prn("site"), access.RoleSiteAdministrator, identity.ServerObject())

	acct := f.accountAdministrator(t, "acct")

	nsAdmin := f.person(t, "nsadmin")
	f.grant(t, prn("nsadmin"), access.RoleAdmin, identity.NamespaceObject("acme"))

	limits := fakeLimits{
		lifted: map[limitKey]bool{
			{group: readers.ID, newName: "", change: principal.GroupChangeAddMember}: true,
		},
		kind: access.LimitKindAccessLimit,
		err:  nil,
	}

	cases := []struct {
		name   string
		caller identity.Principal
		want   error
	}{
		{name: "a site administrator by a stored grant passes", caller: site, want: nil},
		{name: "an account administrator is refused", caller: acct, want: access.ErrEscalation},
		{name: "a namespace administrator is refused", caller: nsAdmin, want: access.ErrEscalation},
		{name: "the bootstrap administrator passes", caller: admin(), want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := serviceWithLimits(t, f, limits)

			err := svc.CheckGroupGrants(ctx, tc.caller, readers.ID, "", principal.GroupChangeAddMember)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckGroupGrants(readers) = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestCheckGroupGrantsRefusesAFailedSiteAdministratorDecision proves that a
// decision of the site administrator actions that fails for another reason
// than a denial refuses the change, and that the refusal carries the error of
// the read.
func TestCheckGroupGrantsRefusesAFailedSiteAdministratorDecision(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")

	// The group holds no grant, so the grants check of the group reads no
	// grant through GrantsOn before the site administrator check.
	ops := f.group(t, "ops")

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: &failingGrantsStore{Store: f.store}, Callers: f.store, Collections: nil, Units: f.store,
		Principals: f.store,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: f.store, Namespaces: f.store, Authorizer: authz,
		Now: func() time.Time { return time.UnixMilli(1_760_000_000_000) }, NewID: nil,
		BootstrapAdmins: []string{"admin"},
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		LimitGroups: fakeLimits{
			lifted: map[limitKey]bool{
				{group: ops.ID, newName: "", change: principal.GroupChangeAddMember}: true,
			},
			kind: access.LimitKindAccessLimit,
			err:  nil,
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	err = svc.CheckGroupGrants(ctx, acct, ops.ID, "", principal.GroupChangeAddMember)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(ops) = %v, want ErrEscalation", err)
	}

	if !errors.Is(err, errGrantsRead) {
		t.Fatalf("CheckGroupGrants(ops) = %v, want the error of the read", err)
	}
}

// TestCheckGroupGrantsRefusalNamesTheLimit proves that the refusal of a
// change that can lift an access limit carries the sentinel that the routes
// map to 403, and names the limit, its scope and the group. The refusal of
// the grants check keeps its own text.
func TestCheckGroupGrantsRefusalNamesTheLimit(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")

	// The group holds site-administrator, which the account administrator
	// does not hold: the grants check refuses the change.
	ops := f.group(t, "ops")
	f.grant(t, ops, access.RoleSiteAdministrator, identity.ServerObject())

	svc := serviceWithLimits(t, f, fakeLimits{
		lifted: map[limitKey]bool{}, kind: access.LimitKindAccessLimit, err: nil,
	})

	grantsErr := svc.CheckGroupGrants(ctx, acct, ops.ID, "", principal.GroupChangeRemoveMember)
	if grantsErr == nil || !errors.Is(grantsErr, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(ops) = %v, want ErrEscalation", grantsErr)
	}

	// The group holds a viewer grant only, and an access limit names it.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	svc = serviceWithLimits(t, f, fakeLimits{
		lifted: map[limitKey]bool{
			{group: readers.ID, newName: "", change: principal.GroupChangeAddMember}: true,
		},
		kind: access.LimitKindAccessLimit,
		err:  nil,
	})

	limitErr := svc.CheckGroupGrants(ctx, acct, readers.ID, "", principal.GroupChangeAddMember)
	if limitErr == nil || !errors.Is(limitErr, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(readers) = %v, want ErrEscalation", limitErr)
	}

	// The two refusals carry the sentinel that the routes map to 403, and
	// each one gives its own reason.
	if limitErr.Error() == grantsErr.Error() {
		t.Fatalf("the refusal of a limit = %q, and the refusal of the grants = %q, want two reasons",
			limitErr, grantsErr)
	}

	for _, want := range []string{`"lim-1"`, "namespace/acme", readers.ID, "add a member"} {
		if !strings.Contains(limitErr.Error(), want) {
			t.Fatalf("the refusal of a limit = %q, want %q", limitErr, want)
		}
	}
}

// TestCheckGroupGrantsRefusalHidesALimitThatTheCallerCannotRead proves that
// the refusal of a change that can lift an access limit names the limit, its
// scope and the group only when the caller may read the gate configuration
// of the namespace of the limit. A caller who cannot read it gets the reason
// without the names.
func TestCheckGroupGrantsRefusalHidesALimitThatTheCallerCannotRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")

	// carol holds no grant, so she may read no gate configuration.
	carol := f.person(t, "carol")

	// readers holds a viewer grant on acme, which the account
	// administrator holds too, so the grants check lets the change pass.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	// ops holds no grant, so the grants check lets carol pass.
	ops := f.group(t, "ops")

	svc := serviceWithLimits(t, f, fakeLimits{
		lifted: map[limitKey]bool{
			{group: readers.ID, newName: "", change: principal.GroupChangeAddMember}: true,
			{group: ops.ID, newName: "", change: principal.GroupChangeAddMember}:     true,
		},
		kind: access.LimitKindAccessLimit,
		err:  nil,
	})

	readErr := svc.CheckGroupGrants(ctx, acct, readers.ID, "", principal.GroupChangeAddMember)
	if readErr == nil {
		t.Fatal("CheckGroupGrants(readers, acct) = nil, want ErrEscalation")
	}

	if !errors.Is(readErr, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(readers, acct) = %v, want ErrEscalation", readErr)
	}

	for _, want := range []string{`"lim-1"`, "namespace/acme", readers.ID, "add a member"} {
		if !strings.Contains(readErr.Error(), want) {
			t.Fatalf("the refusal of a limit that the caller may read = %q, want %q", readErr, want)
		}
	}

	hiddenErr := svc.CheckGroupGrants(ctx, carol, ops.ID, "", principal.GroupChangeAddMember)
	if hiddenErr == nil {
		t.Fatal("CheckGroupGrants(ops, carol) = nil, want ErrEscalation")
	}

	if !errors.Is(hiddenErr, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(ops, carol) = %v, want ErrEscalation", hiddenErr)
	}

	if !strings.Contains(hiddenErr.Error(), "an access limit that you cannot read") {
		t.Fatalf("the refusal of a limit that the caller may not read = %q, want the reason without the names",
			hiddenErr)
	}

	for _, unwanted := range []string{"lim-1", "namespace/acme", "acme"} {
		if strings.Contains(hiddenErr.Error(), unwanted) {
			t.Fatalf("the refusal of a limit that the caller may not read = %q, want no %q",
				hiddenErr, unwanted)
		}
	}
}
