package trigger_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// savedFollows returns a valid follows trigger of unit "b" on unit "a", saved by
// ada.
func savedFollows() trigger.Trigger {
	return follows("f1", "b", "a")
}

func TestNewSaverCheckNeedsPorts(t *testing.T) {
	t.Parallel()

	r, a, u := newResolver(), newAuthz(), newUnits()

	for name, cfg := range map[string]trigger.SaverCheckConfig{
		"no resolver":   {Resolver: nil, Authorizer: a, Units: u},
		"no authorizer": {Resolver: r, Authorizer: nil, Units: u},
		"no units":      {Resolver: r, Authorizer: a, Units: nil},
	} {
		if _, err := trigger.NewSaverCheck(cfg); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: NewSaverCheck = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSaverCheck(t *testing.T) {
	t.Parallel()

	manageAndRun := []identity.Action{trigger.SettingsAction, trigger.StartAction}

	tests := []struct {
		name string
		// setup prepares the ports. The default is a saver ada with the two
		// actions on unit b and unit:read on unit a.
		setup     func(r *fakeResolver, a *fakeAuthz, u *fakeUnits)
		trigger   trigger.Trigger
		wantSave  error
		wantStart error
	}{
		{
			name: "a saver with every action", setup: nil, trigger: savedFollows(), wantSave: nil, wantStart: nil,
		},
		{
			name: "a saver without unit:read on the source is refused at save only",
			setup: func(_ *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				delete(a.allow, ada+" "+string(identity.ActionUnitRead)+" a")
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: nil,
		},
		{
			name: "a schedule trigger needs no source read",
			setup: func(_ *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				delete(a.allow, ada+" "+string(identity.ActionUnitRead)+" a")
			},
			trigger: func() trigger.Trigger {
				tr := schedule("s1", trigger.Frequency{Preset: "daily", Cron: ""})
				tr.Unit = unit("b")

				return tr
			}(),
			wantSave: nil, wantStart: nil,
		},
		{
			name: "a saver that is missing",
			setup: func(r *fakeResolver, _ *fakeAuthz, _ *fakeUnits) {
				delete(r.people, ada)
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a saver that is deleted or disabled",
			setup: func(r *fakeResolver, _ *fakeAuthz, _ *fakeUnits) {
				r.errs[ada] = trigger.ErrSaverGone
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a resolver that fails",
			setup: func(r *fakeResolver, _ *fakeAuthz, _ *fakeUnits) {
				r.errs[ada] = errors.New("database is down")
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a resolver that answers another subject",
			setup: func(r *fakeResolver, _ *fakeAuthz, _ *fakeUnits) {
				r.people[ada] = person(grace)
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "an administrator flag from the resolver counts for nothing",
			setup: func(r *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				admin := person(ada)
				admin.Admin = true
				r.people[ada] = admin
				a.allow = map[string]bool{}
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a saver without the settings action",
			setup: func(_ *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				delete(a.allow, ada+" "+string(trigger.SettingsAction)+" b")
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a saver without the start action",
			setup: func(_ *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				delete(a.allow, ada+" "+string(trigger.StartAction)+" b")
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "an authorizer that fails",
			setup: func(_ *fakeResolver, a *fakeAuthz, _ *fakeUnits) {
				a.fail[ada+" "+string(trigger.StartAction)+" b"] = errors.New("grant store is down")
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a unit that is gone",
			setup: func(_ *fakeResolver, _ *fakeAuthz, u *fakeUnits) {
				u.gone["b"] = true
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: trigger.ErrSaverDenied,
		},
		{
			name: "a source that is gone is refused at save only",
			setup: func(_ *fakeResolver, _ *fakeAuthz, u *fakeUnits) {
				u.gone["a"] = true
			},
			trigger: savedFollows(), wantSave: trigger.ErrSaverDenied, wantStart: nil,
		},
		{
			name: "a trigger with no saver",
			setup: func(*fakeResolver, *fakeAuthz, *fakeUnits) {
			},
			trigger: func() trigger.Trigger { tr := savedFollows(); tr.LastSaver = ""; return tr }(),
			wantSave: trigger.ErrInvalid, wantStart: trigger.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, a, u := newResolver(), newAuthz(), newUnits()
			r.people[ada] = person(ada)
			a.grant(ada, "b", manageAndRun...)
			a.grant(ada, "a", identity.ActionUnitRead)

			if tt.setup != nil {
				tt.setup(r, a, u)
			}

			c, err := trigger.NewSaverCheck(trigger.SaverCheckConfig{Resolver: r, Authorizer: a, Units: u})
			if err != nil {
				t.Fatalf("NewSaverCheck = %v", err)
			}

			wantErr(t, "CheckSave", c.CheckSave(t.Context(), tt.trigger), tt.wantSave)
			wantErr(t, "CheckStart", c.CheckStart(t.Context(), tt.trigger), tt.wantStart)

			// Every refusal is a denial for the caller of the check.
			if tt.wantStart == trigger.ErrSaverDenied {
				if err := c.CheckStart(t.Context(), tt.trigger); !errors.Is(err, identity.ErrDenied) {
					t.Errorf("CheckStart = %v, want it to wrap identity.ErrDenied", err)
				}
			}
		})
	}
}
