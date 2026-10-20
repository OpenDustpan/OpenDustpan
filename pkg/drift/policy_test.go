package drift_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/drift"
)

func TestFrequencyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    drift.Frequency
		ok   bool
	}{
		{"hourly", drift.Frequency{Preset: drift.PresetHourly, Cron: ""}, true},
		{"every 6 hours", drift.Frequency{Preset: drift.PresetEvery6Hours, Cron: ""}, true},
		{"daily", drift.Frequency{Preset: drift.PresetDaily, Cron: ""}, true},
		{"weekly", drift.Frequency{Preset: drift.PresetWeekly, Cron: ""}, true},
		{"cron", drift.Frequency{Preset: "", Cron: "0 3 * * 1-5"}, true},
		{"cron with lists and steps", drift.Frequency{Preset: "", Cron: "0,30 */6 1-15 * *"}, true},
		{"nothing", drift.Frequency{Preset: "", Cron: ""}, false},
		{"both", drift.Frequency{Preset: drift.PresetDaily, Cron: "0 3 * * *"}, false},
		{"unknown preset", drift.Frequency{Preset: "monthly", Cron: ""}, false},
		{"four fields", drift.Frequency{Preset: "", Cron: "0 3 * *"}, false},
		{"six fields", drift.Frequency{Preset: "", Cron: "0 0 3 * * *"}, false},
		{"command in cron", drift.Frequency{Preset: "", Cron: "0 3 * * $(id)"}, false},
		{"newline in cron", drift.Frequency{Preset: "", Cron: "0 3 * *\n*"}, false},
		{"name in cron", drift.Frequency{Preset: "", Cron: "0 3 * * MON"}, false},
		{"long cron", drift.Frequency{Preset: "", Cron: strings.Repeat("* ", drift.MaxCronBytes)}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.f.Validate()

			if tc.ok {
				mustValid(t, err)
			} else {
				mustInvalid(t, err)
			}
		})
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()

	daily := drift.Frequency{Preset: drift.PresetDaily, Cron: ""}
	none := drift.Frequency{Preset: "", Cron: ""}

	tests := []struct {
		name string
		p    drift.Policy
		ok   bool
	}{
		{"the zero policy is off", drift.Policy{Frequency: none, Enabled: false}, true},
		{"enabled with a frequency", drift.Policy{Frequency: daily, Enabled: true}, true},
		{"disabled keeps a frequency", drift.Policy{Frequency: daily, Enabled: false}, true},
		{"enabled with no frequency", drift.Policy{Frequency: none, Enabled: true}, false},
		{
			"disabled with a bad frequency",
			drift.Policy{Frequency: drift.Frequency{Preset: "x", Cron: ""}, Enabled: false},
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.p.Validate()

			if tc.ok {
				mustValid(t, err)
			} else {
				mustInvalid(t, err)
			}
		})
	}
}

func TestScopeValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope drift.Scope
		ok    bool
	}{
		{"namespace", drift.Scope{Namespace: "acme", Kind: drift.ScopeNamespace, Name: ""}, true},
		{"collection", drift.Scope{Namespace: "acme", Kind: drift.ScopeCollection, Name: "payments"}, true},
		{"unit", drift.Scope{Namespace: "acme", Kind: drift.ScopeUnit, Name: "wsp-1"}, true},
		{"no namespace", drift.Scope{Namespace: "", Kind: drift.ScopeNamespace, Name: ""}, false},
		{"namespace with a name", drift.Scope{Namespace: "acme", Kind: drift.ScopeNamespace, Name: "x"}, false},
		{"unit with no name", drift.Scope{Namespace: "acme", Kind: drift.ScopeUnit, Name: ""}, false},
		{
			"collection with a long name",
			drift.Scope{Namespace: "acme", Kind: drift.ScopeCollection, Name: strings.Repeat("a", 101)},
			false,
		},
		{"collection with a newline", drift.Scope{Namespace: "acme", Kind: drift.ScopeCollection, Name: "a\nb"}, false},
		{"unknown kind", drift.Scope{Namespace: "acme", Kind: "installation", Name: ""}, false},
		{"no kind", drift.Scope{Namespace: "acme", Kind: "", Name: ""}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.scope.Validate()

			if tc.ok {
				mustValid(t, err)
			} else {
				mustInvalid(t, err)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	on := drift.Policy{Frequency: drift.Frequency{Preset: drift.PresetDaily, Cron: ""}, Enabled: true}
	weekly := drift.Policy{Frequency: drift.Frequency{Preset: drift.PresetWeekly, Cron: ""}, Enabled: true}
	off := drift.Policy{Frequency: drift.Frequency{Preset: "", Cron: ""}, Enabled: false}

	tests := []struct {
		name                string
		unit, collection, n *drift.Policy
		want                drift.Policy
	}{
		{"no setting is off", nil, nil, nil, off},
		{"namespace default", nil, nil, &on, on},
		{"collection beats namespace", nil, &weekly, &on, weekly},
		{"unit beats collection", &on, &weekly, nil, on},
		{"a disabled unit beats an enabled namespace", &off, nil, &on, off},
		{"a disabled collection beats an enabled namespace", nil, &off, &on, off},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := drift.Resolve(tc.unit, tc.collection, tc.n); got != tc.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
