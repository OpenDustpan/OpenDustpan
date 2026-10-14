package principaltest

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// Sizes of the benchmark installation: hundreds to thousands of users and
// groups, and one user in 80 groups, as a directory sync makes (ADR 55).
const (
	benchPrincipals = 5000
	benchGroups     = 1000
	benchWideGroups = 80
)

// Bench seeds s with 5000 principals, 1000 groups, one membership for each
// principal and 80 groups for the first principal, then measures one page
// of 100 rows and its total for each list. Each store runs it from its own
// benchmark.
func Bench(b *testing.B, s principal.Store) {
	b.Helper()

	seedBench(b, s)

	page := principal.Query{After: "", Low: "", High: "", Limit: principal.DefaultLimit + 1}
	prefixed := principal.Query{After: "", Low: "user-01", High: "user-02", Limit: principal.DefaultLimit + 1}

	cases := []struct {
		run  func() (int, int64, error)
		name string
	}{
		{name: "principals", run: func() (int, int64, error) {
			rows, total, err := s.ListPrincipals(b.Context(), page)

			return len(rows), total, err
		}},
		{name: "principals with a prefix", run: func() (int, int64, error) {
			rows, total, err := s.ListPrincipals(b.Context(), prefixed)

			return len(rows), total, err
		}},
		{name: "groups", run: func() (int, int64, error) {
			rows, total, err := s.ListGroups(b.Context(), page)

			return len(rows), total, err
		}},
		{name: "members of a group", run: func() (int, int64, error) {
			rows, total, err := s.ListMembers(b.Context(), "grp-0000", page)

			return len(rows), total, err
		}},
		{name: "groups of a principal in 80 groups", run: func() (int, int64, error) {
			rows, total, err := s.ListGroupsOf(b.Context(), "prn-00000", page)

			return len(rows), total, err
		}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				if _, _, err := tc.run(); err != nil {
					b.Fatalf("%s: %v", tc.name, err)
				}
			}
		})
	}
}

// seedBench writes the principals, the groups and the memberships of Bench.
func seedBench(b *testing.B, s principal.Store) {
	b.Helper()

	ctx := b.Context()

	for i := range benchGroups {
		g := newGroup(fmt.Sprintf("grp-%04d", i), fmt.Sprintf("team-%04d", i))
		if err := s.CreateGroup(ctx, g, entry()); err != nil {
			b.Fatalf("seed group: %v", err)
		}
	}

	for i := range benchPrincipals {
		id := fmt.Sprintf("prn-%05d", i)

		p := newPrincipal(id, fmt.Sprintf("user-%05d", i), principal.KindPerson)
		if err := s.CreatePrincipal(ctx, p, entry()); err != nil {
			b.Fatalf("seed principal: %v", err)
		}

		groups := 1
		if i == 0 {
			groups = benchWideGroups
		}

		for k := range groups {
			m := principal.Membership{
				AddedAt:     at(1),
				GroupID:     fmt.Sprintf("grp-%04d", (i+k)%benchGroups),
				PrincipalID: id,
			}
			if err := s.AddMember(ctx, m, entry()); err != nil {
				b.Fatalf("seed member: %v", err)
			}
		}
	}
}
