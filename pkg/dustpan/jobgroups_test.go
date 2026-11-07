package dustpan_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/principal"
)

var errPrincipalsDown = errors.New("principal store is down")

// groupsStore is a principal.Store that answers the two reads of the adapter.
// The groups of a principal are in the order of the key of the list: their
// lower-case names.
type groupsStore struct {
	principal.Store

	people map[string]principal.Principal
	groups map[string][]principal.Group
	err    error
	pages  int
}

func (s *groupsStore) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	if s.err != nil {
		return principal.Principal{}, s.err
	}

	p, ok := s.people[subject]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return p, nil
}

func (s *groupsStore) ListGroupsOf(_ context.Context, id string, q principal.Query) ([]principal.Group, int64, error) {
	s.pages++

	if s.err != nil {
		return nil, 0, s.err
	}

	var rows []principal.Group

	for _, g := range s.groups[id] {
		if strings.ToLower(g.Name) > q.After && len(rows) < q.Limit {
			rows = append(rows, g)
		}
	}

	return rows, int64(len(s.groups[id])), nil
}

// person returns a principal record for subject.
func person(subject string) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-" + subject,
		Subject: subject, DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

// disabledPerson returns a principal record for subject that an administrator
// disabled.
func disabledPerson(subject string) principal.Principal {
	p := person(subject)
	p.DisabledAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	return p
}

func groupOf(id, name string) principal.Group {
	return principal.Group{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, ID: id, Name: name, Description: "",
		Source: principal.SourceLocal, MemberCount: 0,
	}
}

func newGroupsStore() *groupsStore {
	return &groupsStore{
		Store: nil,
		people: map[string]principal.Principal{
			"carol": person("carol"), "dave": disabledPerson("dave"),
		},
		groups: map[string][]principal.Group{"prn-carol": {groupOf("grp-1", "platform")}, "prn-dave": {
			groupOf("grp-1", "platform"),
		}},
		err: nil, pages: 0,
	}
}

func TestJobGroupsMatchesByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		ids     []string
		want    bool
	}{
		{"a member", "carol", []string{"grp-1"}, true},
		{"a member of one of several groups", "carol", []string{"grp-7", "grp-1"}, true},
		{"a member of another group", "carol", []string{"grp-2"}, false},
		{"no group in the question", "carol", nil, false},
		{"a disabled member", "dave", []string{"grp-1"}, false},
		{"a subject with no principal", "erin", []string{"grp-1"}, false},
		// The group "platform" of the store has the ID grp-1. A group that
		// someone made again with the same name has another ID, so a gate that
		// names grp-9 does not match it.
		{"a group with the name of the group of the gate and another ID", "carol", []string{"grp-9"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := dustpan.NewJobGroups(newGroupsStore()).MemberOfAny(t.Context(), tc.subject, tc.ids)
			if err != nil || got != tc.want {
				t.Fatalf("MemberOfAny = %v, %v; want %v and no error", got, err, tc.want)
			}
		})
	}
}

func TestJobGroupsKeepsMatchingARenamedGroup(t *testing.T) {
	t.Parallel()

	store := newGroupsStore()
	store.groups["prn-carol"] = []principal.Group{groupOf("grp-1", "platform-engineering")}

	got, err := dustpan.NewJobGroups(store).MemberOfAny(t.Context(), "carol", []string{"grp-1"})
	if err != nil || !got {
		t.Fatalf("MemberOfAny after a rename = %v, %v; want true", got, err)
	}
}

func TestJobGroupsFailsClosed(t *testing.T) {
	t.Parallel()

	store := newGroupsStore()
	store.err = errPrincipalsDown

	got, err := dustpan.NewJobGroups(store).MemberOfAny(t.Context(), "carol", []string{"grp-1"})
	if got || !errors.Is(err, errPrincipalsDown) {
		t.Fatalf("MemberOfAny with a failing store = %v, %v; want false and the failure", got, err)
	}

	got, err = dustpan.NewJobGroups(nil).MemberOfAny(t.Context(), "carol", []string{"grp-1"})
	if got || !errors.Is(err, dustpan.ErrNoPrincipalStore) {
		t.Fatalf("MemberOfAny with no store = %v, %v; want false and ErrNoPrincipalStore", got, err)
	}
}

func TestJobGroupsReadsEveryPageUpToTheBound(t *testing.T) {
	t.Parallel()

	many := func(n int) []principal.Group {
		groups := make([]principal.Group, n)
		for i := range groups {
			groups[i] = groupOf(fmt.Sprintf("grp-%05d", i), fmt.Sprintf("g%05d", i))
		}

		return groups
	}

	// A match on the last group of the third page.
	store := newGroupsStore()
	store.groups["prn-carol"] = many(2*dustpan.JobGroupsPage + 5)

	got, err := dustpan.NewJobGroups(store).MemberOfAny(t.Context(), "carol",
		[]string{fmt.Sprintf("grp-%05d", 2*dustpan.JobGroupsPage+4)})
	if err != nil || !got || store.pages != 3 {
		t.Fatalf("MemberOfAny = %v, %v after %d pages; want true after 3", got, err, store.pages)
	}

	// More groups than the bound covers, and no match: an error, not false.
	store = newGroupsStore()
	store.groups["prn-carol"] = many(dustpan.JobGroupsMaxPages*dustpan.JobGroupsPage + 1)

	got, err = dustpan.NewJobGroups(store).MemberOfAny(t.Context(), "carol", []string{"grp-none"})
	if got || !errors.Is(err, dustpan.ErrTooManyGroups) {
		t.Fatalf("MemberOfAny past the bound = %v, %v; want false and ErrTooManyGroups", got, err)
	}
}
