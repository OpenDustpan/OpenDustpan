package trigger_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func TestNewOutputReadCheckNeedsPorts(t *testing.T) {
	t.Parallel()

	s, r, a := newStore(), newResolver(), newAuthz()

	for name, args := range map[string]struct {
		store trigger.Store
		res   trigger.SaverResolver
		authz identity.Authorizer
	}{
		"no store":      {nil, r, a},
		"no resolver":   {s, nil, a},
		"no authorizer": {s, r, nil},
	} {
		if _, err := trigger.NewOutputReadCheck(args.store, args.res, args.authz); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: NewOutputReadCheck = %v, want ErrInvalid", name, err)
		}
	}
}

func TestOutputReadCheck(t *testing.T) {
	t.Parallel()

	sourceObj := func() identity.Object {
		obj, err := identity.UnitObject("opentofu", acme, "a")
		if err != nil {
			t.Fatalf("UnitObject = %v", err)
		}

		return obj.WithUnitID("a")
	}

	read := identity.ActionUnitRead
	secrets := identity.ActionUnitReadSecrets

	base := func() trigger.OutputRead {
		return trigger.OutputRead{
			Namespace: acme, Trigger: "f1", Unit: unit("b"), Source: sourceObj(), Secret: false, Caller: nil,
		}
	}

	callerOf := func(p identity.Principal) *identity.Principal { return &p }

	tests := []struct {
		name  string
		setup func(r *fakeResolver, a *fakeAuthz, s *fakeStore)
		read  func() trigger.OutputRead
		want  bool
	}{
		{"the saver reads a plain output", nil, base, true},
		{
			"the saver reads a secret output with unit:read-secrets",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) { a.grant(ada, "a", secrets) },
			func() trigger.OutputRead { in := base(); in.Secret = true; return in }, true,
		},
		{
			"the saver has no unit:read-secrets for a secret output", nil,
			func() trigger.OutputRead { in := base(); in.Secret = true; return in }, false,
		},
		{
			"the saver lost unit:read",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) { delete(a.allow, ada+" "+string(read)+" a") },
			base, false,
		},
		{
			"the saver is missing, deleted or disabled",
			func(r *fakeResolver, _ *fakeAuthz, _ *fakeStore) { delete(r.people, ada) }, base, false,
		},
		{
			"the resolver fails",
			func(r *fakeResolver, _ *fakeAuthz, _ *fakeStore) { r.errs[ada] = errDBDown },
			base, false,
		},
		{
			"the resolver answers another subject",
			func(r *fakeResolver, _ *fakeAuthz, _ *fakeStore) { r.people[ada] = person(grace) }, base, false,
		},
		{
			"the administrator flag of the resolver is never trusted",
			func(r *fakeResolver, a *fakeAuthz, _ *fakeStore) {
				admin := person(ada)
				admin.Admin = true
				r.people[ada] = admin
				a.allow = map[string]bool{}
			},
			base, false,
		},
		{
			"the authorizer fails",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) {
				a.fail[ada+" "+string(read)+" a"] = errDBDown
			},
			base, false,
		},
		{
			"the trigger is gone",
			func(_ *fakeResolver, _ *fakeAuthz, s *fakeStore) { delete(s.triggers, "f1") }, base, false,
		},
		{
			"the trigger does not start the unit of the job", nil,
			func() trigger.OutputRead { in := base(); in.Unit = unit("c"); return in }, false,
		},
		{
			"the unit is in another namespace", nil,
			func() trigger.OutputRead {
				in := base()
				in.Unit = graph.Unit{Namespace: "other", Tool: "opentofu", ID: "b"}

				return in
			}, false,
		},
		{
			"the trigger ID is not valid", nil,
			func() trigger.OutputRead { in := base(); in.Trigger = "F1 "; return in }, false,
		},
		{
			"the source is not a unit", nil,
			func() trigger.OutputRead { in := base(); in.Source = identity.NamespaceObject(acme); return in }, false,
		},
		{
			"the source is in another namespace",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) { a.grant(ada, "a", read) },
			func() trigger.OutputRead {
				in := base()

				obj, err := identity.UnitObject("opentofu", "other", "a")
				if err != nil {
					t.Fatalf("UnitObject = %v", err)
				}

				in.Source = obj

				return in
			}, false,
		},
		{
			"a manual fire: both the caller and the saver may read",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) { a.grant(grace, "a", read) },
			func() trigger.OutputRead { in := base(); in.Caller = callerOf(person(grace)); return in }, true,
		},
		{
			"a manual fire: a caller without unit:read gains nothing through the trigger", nil,
			func() trigger.OutputRead { in := base(); in.Caller = callerOf(person(grace)); return in }, false,
		},
		{
			"a manual fire: the caller lacks unit:read-secrets for a secret output",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) {
				a.grant(ada, "a", secrets)
				a.grant(grace, "a", read)
			},
			func() trigger.OutputRead {
				in := base()
				in.Secret = true
				in.Caller = callerOf(person(grace))

				return in
			}, false,
		},
		{
			"a manual fire: the saver lost access though the caller keeps it",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) {
				a.grant(grace, "a", read)
				delete(a.allow, ada+" "+string(read)+" a")
			},
			func() trigger.OutputRead {
				in := base()
				in.Caller = callerOf(person(grace))

				return in
			}, false,
		},
		{
			"a manual fire by an administrator is bound by the saver",
			func(_ *fakeResolver, a *fakeAuthz, _ *fakeStore) { delete(a.allow, ada+" "+string(read)+" a") },
			func() trigger.OutputRead {
				admin := person(grace)
				admin.Admin = true

				in := base()
				in.Caller = &admin

				return in
			}, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, r, a := newStore(), newResolver(), newAuthz()
			s.triggers["f1"] = follows("f1", "b", "a")
			r.people[ada] = person(ada)
			a.grant(ada, "a", read)

			if tt.setup != nil {
				tt.setup(r, a, s)
			}

			c, err := trigger.NewOutputReadCheck(s, r, a)
			if err != nil {
				t.Fatalf("NewOutputReadCheck = %v", err)
			}

			err = c.Allow(t.Context(), tt.read())

			if tt.want {
				checkAllowed(t, err)
			} else {
				checkRefused(t, err)
			}
		})
	}
}

// checkAllowed checks that err is nil.
func checkAllowed(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Errorf("Allow = %v, want it allowed", err)
	}
}

// checkRefused checks that err wraps identity.ErrDenied.
func checkRefused(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, identity.ErrDenied) {
		t.Errorf("Allow = %v, want it to wrap identity.ErrDenied", err)
	}
}
