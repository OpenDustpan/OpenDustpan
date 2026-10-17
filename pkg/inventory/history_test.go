package inventory_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
)

// change returns a change log row of the unit id of tool at minute min.
func change(tool, id, name string, kind inventory.ChangeKind, minute int) inventory.Change {
	return inventory.Change{
		At:            time.Date(2026, time.October, 17, 9, minute, 0, 0, time.UTC),
		Namespace:     "acme",
		Tool:          tool,
		UnitID:        id,
		Name:          name,
		DisplayName:   name,
		Status:        "",
		Collection:    "",
		Kind:          kind,
		ResourceCount: nil,
	}
}

func TestReplayAt(t *testing.T) {
	t.Parallel()

	log := []inventory.Change{
		change("pulumi", "stk-a", "web/prod", inventory.ChangeCreated, 1),
		change("opentofu", "ws-b", "network", inventory.ChangeCreated, 2),
		change("pulumi", "stk-a", "web/live", inventory.ChangeRenamed, 3),
		change("opentofu", "ws-b", "network", inventory.ChangeDeleted, 4),
		change("opentofu", "ws-b", "network", inventory.ChangeRestored, 5),
		change("pulumi", "stk-c", "api/prod", inventory.ChangeCreated, 6),
		change("pulumi", "stk-c", "api/prod", inventory.ChangePurged, 7),
		change("pulumi", "stk-d", "web/prod", inventory.ChangeCreated, 8),
	}

	at := func(minute int) time.Time { return time.Date(2026, time.October, 17, 9, minute, 0, 0, time.UTC) }

	cases := []struct {
		at   time.Time
		name string
		want []string
	}{
		{name: "before the first row", at: at(0), want: []string{}},
		{name: "at the time of a row", at: at(1), want: []string{"pulumi:web/prod:stk-a"}},
		{name: "before a rename", at: at(2), want: []string{"opentofu:network:ws-b", "pulumi:web/prod:stk-a"}},
		{name: "after a rename", at: at(3), want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"}},
		{name: "a deleted unit is left out", at: at(4), want: []string{"pulumi:web/live:stk-a"}},
		{
			name: "a restored unit comes back",
			at:   at(5),
			want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"},
		},
		{
			name: "a unit before its purge",
			at:   at(6),
			want: []string{"opentofu:network:ws-b", "pulumi:api/prod:stk-c", "pulumi:web/live:stk-a"},
		},
		{
			name: "a purged unit is left out",
			at:   at(7),
			want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"},
		},
		{
			name: "a new unit takes a free name",
			at:   at(9),
			want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a", "pulumi:web/prod:stk-d"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := []string{}
			for _, e := range inventory.ReplayAt(log, tc.at) {
				got = append(got, e.Tool+":"+e.Name+":"+e.UnitID)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("ReplayAt = %v, want %v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ReplayAt = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestReplayAtTakesAnyOrder(t *testing.T) {
	t.Parallel()

	log := []inventory.Change{
		change("pulumi", "stk-a", "web/prod", inventory.ChangeDeleted, 5),
		change("pulumi", "stk-a", "web/prod", inventory.ChangeCreated, 1),
	}

	if got := inventory.ReplayAt(log, time.Date(2026, time.October, 17, 9, 3, 0, 0, time.UTC)); len(got) != 1 {
		t.Errorf("ReplayAt between create and delete = %v, want the unit", got)
	}
}
