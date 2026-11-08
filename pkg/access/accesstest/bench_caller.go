package accesstest

import (
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Sizes of the caller-grants benchmark: a caller in 75 groups with 4
// grants each and 100 grants of its own, and the other grants of the
// installation in three steps.
const (
	benchCallerGroups      = 75
	benchCallerGroupGrants = 4
	benchCallerOwnGrants   = 100
)

// benchOtherGrants are the counts of the grants of other principals that
// the caller-grants benchmark measures against, in order.
//
//nolint:gochecknoglobals // A constant list; Go has no constant slices.
var benchOtherGrants = []int{0, 20_000, 100_000}

// BenchCallerGrants seeds s with a caller in 75 groups, 4 grants for each
// group and 100 grants of its own, and measures the first page of the
// grants of the caller, with its total, against 0, 20000 and 100000 grants
// of other principals.
func BenchCallerGrants(b *testing.B, s BenchStore) {
	b.Helper()

	svc := seedBenchCaller(b, s)
	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "me", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}

	seeded := 0

	for _, others := range benchOtherGrants {
		for ; seeded < others; seeded++ {
			subject := access.Subject{Kind: access.SubjectPrincipal, ID: fmt.Sprintf("prn-x%06d", seeded)}
			benchGrant(b, s, fmt.Sprintf("grt-x%06d", seeded), subject, fmt.Sprintf("x%06d", seeded))
		}

		b.Run(fmt.Sprintf("others=%d", others), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				page, err := svc.CallerGrants(b.Context(), caller, access.CallerGrantsRequest{Cursor: "", Limit: 0})
				if err != nil || page.Total == nil || *page.Total != benchCallerRows {
					b.Fatalf("caller grants = %v, %v; want a total of %d", page.Total, err, benchCallerRows)
				}
			}
		})
	}
}

// benchCallerRows is the count of the rows of the caller of
// BenchCallerGrants.
const benchCallerRows = benchCallerGroups*benchCallerGroupGrants + benchCallerOwnGrants

// seedBenchCaller writes the namespace, the caller, its groups and its
// grants, and returns the service that reads them.
func seedBenchCaller(b *testing.B, s BenchStore) *access.Service {
	b.Helper()

	ctx := b.Context()

	if err := s.CreateNamespace(ctx, newNamespace("bench")); err != nil {
		b.Fatalf("seed namespace: %v", err)
	}

	me := principal.Principal{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		ID:          "prn-me",
		Subject:     "me",
		DisplayName: "",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0, BreakGlass: false,
		DisabledAt: time.Time{},
	}
	if err := s.CreatePrincipal(ctx, me, entry()); err != nil {
		b.Fatalf("seed principal: %v", err)
	}

	for i := range benchCallerGroups {
		id := fmt.Sprintf("grp-me%02d", i)

		g := principal.Group{
			CreatedAt: at(0), UpdatedAt: at(0), ID: id, Name: "team-" + id, Description: "",
			Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
		if err := s.CreateGroup(ctx, g, entry()); err != nil {
			b.Fatalf("seed group: %v", err)
		}

		m := principal.Membership{
			AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: id, PrincipalID: "prn-me",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		}
		if err := s.AddMember(ctx, m, entry(), nil); err != nil {
			b.Fatalf("seed member: %v", err)
		}

		for k := range benchCallerGroupGrants {
			subject := access.Subject{Kind: access.SubjectGroup, ID: id}
			benchGrant(b, s, fmt.Sprintf("grt-%s-%d", id, k), subject, fmt.Sprintf("%s-%d", id, k))
		}
	}

	for i := range benchCallerOwnGrants {
		subject := access.Subject{Kind: access.SubjectPrincipal, ID: "prn-me"}
		benchGrant(b, s, fmt.Sprintf("grt-me%03d", i), subject, fmt.Sprintf("me%03d", i))
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: s, Callers: s, Collections: nil, Units: s, Principals: s,
	})
	if err != nil {
		b.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: s, Directory: s, Namespaces: s, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: s, Roles: s, Callers: s, Principals: s, Collections: nil, Units: s,
		},
		Now: nil, NewID: nil, BootstrapAdmins: nil,
		LimitGroups: nil,
	})
	if err != nil {
		b.Fatalf("access service: %v", err)
	}

	return svc
}

// benchGrant writes the viewer grant id of subject on the collection with
// the name collection in the namespace bench.
func benchGrant(b *testing.B, s BenchStore, id string, subject access.Subject, collection string) {
	b.Helper()

	obj := identity.CollectionObject(namespace.Name("bench"), collection).WithCollectionID("col-" + collection)
	g := access.Grant{
		CreatedAt: at(2), Condition: nil, Object: obj, ID: id, Role: access.RoleViewer, CreatedBy: "admin",
		Subject: subject,
	}

	if err := s.CreateGrant(b.Context(), g, newNamespace("bench").Fence(), scoped(entry(), "bench"), nil); err != nil {
		b.Fatalf("seed grant %s: %v", id, err)
	}
}
