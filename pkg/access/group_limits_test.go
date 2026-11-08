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

// limitKey names one answer of fakeLimits.
type limitKey struct {
	group  string
	change principal.GroupChange
}

// fakeLimits is an access.LimitGroups that answers from a table, or with an
// error.
type fakeLimits struct {
	lifted map[limitKey]bool
	err    error
}

func (f fakeLimits) LimitLiftedBy(
	_ context.Context, groupID string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	if f.err != nil {
		return access.LimitRef{ID: "", Scope: ""}, false, f.err
	}

	if !f.lifted[limitKey{group: groupID, change: change}] {
		return access.LimitRef{ID: "", Scope: ""}, false, nil
	}

	return access.LimitRef{ID: "lim-1", Scope: "namespace/acme"}, true, nil
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
			table[limitKey{group: readers.ID, change: change}] = true
		}

		return fakeLimits{lifted: table, err: nil}
	}

	cases := []struct {
		name   string
		caller identity.Principal
		limits access.LimitGroups
		change principal.GroupChange
		// text is the change that a refusal names.
		text string
		want error
	}{
		{
			name:   "an add to a group of an unless clause is refused",
			caller: acct,
			limits: lifted(principal.GroupChangeAddMember),
			change: principal.GroupChangeAddMember,
			text:   "add a member",
			want:   access.ErrEscalation,
		},
		{
			name:   "a remove from a group of a when clause is refused",
			caller: acct,
			limits: lifted(principal.GroupChangeRemoveMember),
			change: principal.GroupChangeRemoveMember,
			text:   "remove a member",
			want:   access.ErrEscalation,
		},
		{
			name:   "a remove from a group of an unless clause keeps the grants check",
			caller: acct,
			limits: lifted(principal.GroupChangeAddMember),
			change: principal.GroupChangeRemoveMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "an add to a group of a when clause keeps the grants check",
			caller: acct,
			limits: lifted(principal.GroupChangeRemoveMember),
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "the delete of a group of a when clause is refused",
			caller: acct,
			limits: lifted(principal.GroupChangeDelete),
			change: principal.GroupChangeDelete,
			text:   "delete the group",
			want:   access.ErrEscalation,
		},
		{
			name:   "an administrator adds a member of a group of an unless clause",
			caller: admin(),
			limits: lifted(principal.GroupChangeAddMember),
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "an administrator removes a member of a group of a when clause",
			caller: admin(),
			limits: lifted(principal.GroupChangeRemoveMember),
			change: principal.GroupChangeRemoveMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "an administrator deletes a group of a when clause",
			caller: admin(),
			limits: lifted(principal.GroupChangeDelete),
			change: principal.GroupChangeDelete,
			text:   "",
			want:   nil,
		},
		{
			name:   "a group that no limit names keeps the grants check",
			caller: acct,
			limits: fakeLimits{lifted: map[limitKey]bool{}, err: nil},
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "a limit that names another group keeps the grants check",
			caller: acct,
			limits: fakeLimits{
				lifted: map[limitKey]bool{{group: "grp-other", change: principal.GroupChangeAddMember}: true},
				err:    nil,
			},
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   nil,
		},
		{
			name:   "the read of the limits fails and refuses the change",
			caller: acct,
			limits: fakeLimits{lifted: nil, err: errLimitsRead},
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   access.ErrEscalation,
		},
		{
			name:   "a limit that does not parse refuses the change",
			caller: acct,
			limits: fakeLimits{lifted: nil, err: errLimitParse},
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   access.ErrEscalation,
		},
		{
			name:   "a service without the port keeps the grants check",
			caller: acct,
			limits: nil,
			change: principal.GroupChangeAddMember,
			text:   "",
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := serviceWithLimits(t, f, tc.limits)

			err := svc.CheckGroupGrants(ctx, tc.caller, readers.ID, tc.change)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckGroupGrants(readers, %d) = %v, want %v", tc.change, err, tc.want)
			}

			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("the refusal = %q, want %q", err, tc.text)
			}
		})
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

	svc := serviceWithLimits(t, f, fakeLimits{lifted: map[limitKey]bool{}, err: nil})

	grantsErr := svc.CheckGroupGrants(ctx, acct, ops.ID, principal.GroupChangeRemoveMember)
	if grantsErr == nil || !errors.Is(grantsErr, access.ErrEscalation) {
		t.Fatalf("CheckGroupGrants(ops) = %v, want ErrEscalation", grantsErr)
	}

	// The group holds a viewer grant only, and an access limit names it.
	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	svc = serviceWithLimits(t, f, fakeLimits{
		lifted: map[limitKey]bool{
			{group: readers.ID, change: principal.GroupChangeAddMember}: true,
		},
		err: nil,
	})

	limitErr := svc.CheckGroupGrants(ctx, acct, readers.ID, principal.GroupChangeAddMember)
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
