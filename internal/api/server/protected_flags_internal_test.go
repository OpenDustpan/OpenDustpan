package server

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestGroupsWithFlagsRefusesAMissingFlag gives the page builder a result of
// the batch check that holds no flag for one group of the page, as if the
// batch check had failed to answer for it. The page answers an error, never
// protected: false for that group.
func TestGroupsWithFlagsRefusesAMissingFlag(t *testing.T) {
	t.Parallel()

	group := func(id string) principal.Group {
		return principal.Group{
			CreatedAt: time.Time{}, UpdatedAt: time.Time{}, ID: id, Name: id, Description: "",
			Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
	}

	page := principal.Page[principal.Group]{
		Items: []principal.Group{group("grp-a"), group("grp-b")}, NextCursor: "", Total: 2,
	}

	cases := []struct {
		name  string
		flags map[string]bool
	}{
		{"no flag for the second group", map[string]bool{"grp-a": true}},
		{"no flag for any group", map[string]bool{}},
		{"a nil result", nil},
		{"a flag for another group only", map[string]bool{"grp-a": false, "grp-c": true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := groupsWithFlags(page, tc.flags)
			if !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("groups with %s = %v, want access.ErrInvalid", tc.name, err)
			}

			if len(got.Groups) != 0 {
				t.Fatalf("groups with %s answered %d groups with an error, want none", tc.name, len(got.Groups))
			}
		})
	}

	got, err := groupsWithFlags(page, map[string]bool{"grp-a": true, "grp-b": false})
	if err != nil {
		t.Fatalf("groups with every flag: %v", err)
	}

	if len(got.Groups) != 2 || !got.Groups[0].Protected || got.Groups[1].Protected {
		t.Fatalf("groups with every flag = %+v, want grp-a protected and grp-b not", got.Groups)
	}
}
