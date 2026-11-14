package accesstest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Sizes of the benchmark installation: thousands of principals and units,
// a thousand groups, and one principal in 80 groups, as a directory sync
// makes (ADR 55).
const (
	benchPrincipals  = 5000
	benchGroups      = 1000
	benchWideGroups  = 80
	benchNamespaces  = 10
	benchCollections = 10 // in each namespace
	benchUnits       = 10_000
	benchGrants      = 5000
)

// BenchStore is a store that keeps namespaces, principals, roles, grants
// and Pulumi stacks, answers the subjects of a caller and the stable ID of a
// unit. Both SQL stores are one.
type BenchStore interface {
	access.Store
	access.Callers
	access.Units
	principal.Store
	namespace.Store
	CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
}

// benchCollectionsOf is the collection of each benchmark unit.
type benchCollectionsOf map[string]string

func (c benchCollectionsOf) CollectionOf(_ context.Context, unit identity.Object) (string, bool, error) {
	name, ok := c[unit.String()]

	return name, ok, nil
}

// CollectionID gives a collection that some unit is in the stable ID col-<name>.
func (c benchCollectionsOf) CollectionID(_ context.Context, col identity.Object) (string, bool, error) {
	for _, name := range c {
		if name == col.CollectionName() {
			return "col-" + name, true, nil
		}
	}

	return "", false, nil
}

// Bench seeds s with 5000 principals, 1000 groups, one membership for each
// principal, 80 groups for the first principal, 10000 units in 100
// collections over 10 namespaces, and 5000 grants spread over the
// installation, the namespaces, the collections and the units. It then
// measures one decision on a unit (each in a new request) and one
// list-objects, for the principal in 80 groups.
func Bench(b *testing.B, s BenchStore) {
	b.Helper()

	units, collections := seedBenchInstallation(b, s)

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: s, Callers: s, Collections: collections, Units: s, Principals: s,
	})
	if err != nil {
		b.Fatalf("authorizer: %v", err)
	}

	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "user-00000",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	b.Run("check", func(b *testing.B) {
		b.ReportAllocs()

		i := 0
		for b.Loop() {
			ctx := access.WithRequestCache(b.Context())

			err := authz.Authorize(ctx, caller, identity.ActionUnitRead, units[i%len(units)])
			if err != nil && !isDenied(err) {
				b.Fatalf("authorize: %v", err)
			}

			i++
		}
	})

	b.Run("list objects", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			ctx := access.WithRequestCache(b.Context())

			if _, err := authz.ListObjects(ctx, caller, identity.ActionUnitRead); err != nil {
				b.Fatalf("list objects: %v", err)
			}
		}
	})
}

// seedBenchInstallation writes the installation of Bench and returns its
// units and their collections.
func seedBenchInstallation(b *testing.B, s BenchStore) ([]identity.Object, benchCollectionsOf) {
	b.Helper()

	ctx := b.Context()

	nss := make([]namespace.Name, 0, benchNamespaces)
	fences := make([]namespace.Fence, 0, benchNamespaces)

	for i := range benchNamespaces {
		ns := newNamespace(namespace.Name(fmt.Sprintf("ns%02d", i)))
		if err := s.CreateNamespace(ctx, ns); err != nil {
			b.Fatalf("seed namespace: %v", err)
		}

		nss = append(nss, ns.Name)
		fences = append(fences, ns.Fence())
	}

	units := make([]identity.Object, 0, benchUnits)
	collections := benchCollectionsOf{}

	for i := range benchUnits {
		u, err := identity.UnitObject("pulumi", nss[i%benchNamespaces], fmt.Sprintf("app%05d/prod", i))
		if err != nil {
			b.Fatalf("unit object: %v", err)
		}

		id := fmt.Sprintf("stk-bench%05d", i)
		if err := s.CreateStack(ctx, benchStack(b, u, id), fences[i%benchNamespaces]); err != nil {
			b.Fatalf("seed stack: %v", err)
		}

		collections[u.String()] = fmt.Sprintf("coll%02d", (i/benchNamespaces)%benchCollections)
		// A route resolves a unit by name, so the decision reads the ID.
		units = append(units, u)
	}

	seedBenchPrincipals(b, s)
	seedBenchGrants(b, s, nss, units)

	return units, collections
}

// seedBenchPrincipals writes the principals, the groups and the memberships
// of Bench.
func seedBenchPrincipals(b *testing.B, s BenchStore) {
	b.Helper()

	ctx := b.Context()

	for i := range benchGroups {
		g := principal.Group{
			CreatedAt: at(0), UpdatedAt: at(0), ID: fmt.Sprintf("grp-%04d", i), Name: fmt.Sprintf("team-%04d", i),
			Description: "", Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
		if err := s.CreateGroup(ctx, g, entry()); err != nil {
			b.Fatalf("seed group: %v", err)
		}
	}

	for i := range benchPrincipals {
		id := fmt.Sprintf("prn-%05d", i)

		p := principal.Principal{
			CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: fmt.Sprintf("user-%05d", i), DisplayName: "",
			Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
			DisabledAt: time.Time{},
		}
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
				SuspendedAt: time.Time{},
				GroupID:     fmt.Sprintf("grp-%04d", (i+k)%benchGroups),
				PrincipalID: id,
				Source:      principal.MembershipSourceLocal,
				SourceID:    "",
				State:       principal.MembershipStateActive,
				Pinned:      false,
			}
			if err := s.AddMember(ctx, m, entry(), nil); err != nil {
				b.Fatalf("seed member: %v", err)
			}
		}
	}
}

// seedBenchGrants writes the grants of Bench: half to principals and half
// to groups, a quarter each on namespaces, collections and two sets of
// units.
func seedBenchGrants(b *testing.B, s BenchStore, nss []namespace.Name, units []identity.Object) {
	b.Helper()

	roles := []string{access.RoleViewer, access.RoleWriter, access.RoleMaintainer}

	for i := range benchGrants {
		subject := access.Subject{Kind: access.SubjectPrincipal, ID: fmt.Sprintf("prn-%05d", i%benchPrincipals)}
		if i%2 == 1 {
			subject = access.Subject{Kind: access.SubjectGroup, ID: fmt.Sprintf("grp-%04d", i%benchGroups)}
		}

		var obj identity.Object

		switch i % 4 {
		case 0:
			k := (i * 7919) % benchUnits
			obj = units[k].WithUnitID(fmt.Sprintf("stk-bench%05d", k))
		case 1:
			name := fmt.Sprintf("coll%02d", (i/4)%benchCollections)
			obj = identity.CollectionObject(nss[i%benchNamespaces], name).WithCollectionID("col-" + name)
		case 2:
			obj = identity.NamespaceObject(nss[i%benchNamespaces])
		default:
			k := (i * 104729) % benchUnits
			obj = units[k].WithUnitID(fmt.Sprintf("stk-bench%05d", k))
		}

		g := access.Grant{
			CreatedAt: at(1), Condition: nil, Object: obj, ID: fmt.Sprintf("grt-bench-%05d", i),
			Role: roles[i%len(roles)], CreatedBy: "admin", Subject: subject,
		}

		// A repeated tuple is refused, as the API refuses it. The bench
		// keeps the first.
		fence := newNamespace(obj.Namespace()).Fence()
		if err := s.CreateGrant(b.Context(), g, fence, scoped(entry(), obj.Namespace()), nil); err != nil &&
			!errors.Is(err, access.ErrAlreadyExists) {
			b.Fatalf("seed grant: %v", err)
		}
	}
}

// isDenied reports whether err is a refused decision.
func isDenied(err error) bool {
	return errors.Is(err, identity.ErrDenied)
}

// benchStack returns the Pulumi stack of the unit u, with the stable ID id.
func benchStack(b *testing.B, u identity.Object, id string) stack.Stack {
	b.Helper()

	project, name, _ := strings.Cut(u.UnitName(), "/")

	ref, err := stack.ParseRef(u.Namespace().String(), project, name)
	if err != nil {
		b.Fatalf("stack ref: %v", err)
	}

	return stack.Stack{
		NamespaceID: "",
		CreatedAt:   at(0), UpdatedAt: at(0), Tags: map[string]string{}, Ref: ref, ID: stack.ID(id), Version: 0,
	}
}
