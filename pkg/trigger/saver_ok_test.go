package trigger_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestSaverOKFollowsTheSourceRead saves b follows a as ada, then takes away
// grants of ada. SaverOK reports false, so the trigger needs a new save, when
// ada loses unit:read on the source or an action on the unit.
func TestSaverOKFollowsTheSourceRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		revoke string
		wantOK bool
	}{
		{name: "every grant kept", revoke: "", wantOK: true},
		{name: "no read on the source", revoke: ada + " " + string(identity.ActionUnitRead) + " a", wantOK: false},
		{name: "no start action on the unit", revoke: ada + " " + string(trigger.StartAction) + " b", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, a, u := newResolver(), newAuthz(), newUnits()
			r.people[ada] = person(ada)
			a.grant(ada, "b", trigger.SettingsAction, trigger.StartAction, identity.ActionUnitRead)
			a.grant(ada, "a", identity.ActionUnitRead)

			savers, err := trigger.NewSaverCheck(trigger.SaverCheckConfig{Resolver: r, Authorizer: a, Units: u})
			if err != nil {
				t.Fatalf("NewSaverCheck = %v", err)
			}

			m, err := trigger.NewManager(trigger.ManagerConfig{
				Store: newStore(), Dispatcher: newDispatcher(), Authorizer: a, Savers: savers, Units: u,
				Fences: newFences(), Now: func() time.Time { return time.UnixMilli(1_800_000_000_000).UTC() },
				NewID: func() trigger.ID { return "trg-1" },
			})
			if err != nil {
				t.Fatalf("NewManager = %v", err)
			}

			saved, err := m.Create(t.Context(), person(ada), reqInfo(), followsInput("b", "a"))
			if err != nil {
				t.Fatalf("Create = %v", err)
			}

			delete(a.allow, tc.revoke)

			if got := m.SaverOK(t.Context(), &saved); got != tc.wantOK {
				t.Errorf("SaverOK = %t, want %t", got, tc.wantOK)
			}
		})
	}
}
