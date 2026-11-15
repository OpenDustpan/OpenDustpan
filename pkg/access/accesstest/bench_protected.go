package accesstest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Protected groups of BenchProtectedGroups: every benchProtectedEvery-th
// group holds an operator grant on the installation, and every
// benchActAsEvery-th group can act as a service principal that holds one.
const (
	benchProtectedEvery = 50
	benchActAsEvery     = 100
)

// BenchProtectedGroups seeds s as Bench does, gives some groups a grant that
// protects them, and measures the protected flag of all 1000 groups: one
// group at a time with Protected, as the group list read it before, and the
// whole page with one ProtectedGroups call. It checks first that both answer
// the same flags. The reads/op metric counts the calls of the reach ports.
func BenchProtectedGroups(b *testing.B, s BenchStore) {
	b.Helper()

	_, collections := seedBenchInstallation(b, s)
	seedBenchProtection(b, s)

	ids := make([]string, 0, benchGroups)
	for i := range benchGroups {
		ids = append(ids, fmt.Sprintf("grp-%04d", i))
	}

	reads := &readCount{n: 0}
	cfg := countingReach(access.ReachConfig{
		Grants: s, Roles: s, Callers: s, Principals: s, Collections: collections, Units: s,
	}, reads)

	perGroup := func(ctx context.Context) map[string]bool {
		out := make(map[string]bool, len(ids))

		for _, id := range ids {
			protected, err := access.Protected(ctx, cfg, id)
			if err != nil {
				b.Fatalf("protected of %s: %v", id, err)
			}

			out[id] = protected
		}

		return out
	}

	page := func(ctx context.Context) map[string]bool {
		out, err := access.ProtectedGroups(ctx, cfg, ids)
		if err != nil {
			b.Fatalf("protected groups: %v", err)
		}

		return out
	}

	one, all := perGroup(b.Context()), page(b.Context())
	if !maps.Equal(one, all) {
		b.Fatal("ProtectedGroups answers other flags than Protected for one group at a time")
	}

	if protected := countTrue(all); protected == 0 || protected == len(all) {
		b.Fatalf("%d of %d groups are protected, want some and not all", protected, len(all))
	}

	for _, run := range []struct {
		name string
		fn   func(ctx context.Context) map[string]bool
	}{{"one group at a time", perGroup}, {"one page", page}} {
		b.Run(run.name, func(b *testing.B) {
			b.ReportAllocs()

			reads.n = 0

			for b.Loop() {
				run.fn(b.Context())
			}

			b.ReportMetric(float64(reads.n)/float64(b.N), "reads/op")
		})
	}
}

// seedBenchProtection adds the grants that protect some groups of Bench: an
// operator grant on the installation for every benchProtectedEvery-th group,
// and a token manager grant on a service principal with an operator grant for
// every benchActAsEvery-th group, so that the walk follows an act-as edge.
func seedBenchProtection(b *testing.B, s BenchStore) {
	b.Helper()

	ctx := b.Context()
	server := identity.ServerObject()
	none := namespace.Fence{Namespace: "", FailoverVersion: 0}

	svc := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "prn-bench-svc", Subject: "ci-bench", DisplayName: "",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{}, BreakGlass: false,
	}
	if err := s.CreatePrincipal(ctx, svc, entry()); err != nil {
		b.Fatalf("seed service principal: %v", err)
	}

	grants := []access.Grant{
		protectionGrant("grt-bench-svc", principalSubject(svc.ID), access.RoleInstallationOperator, server),
	}

	for i := 0; i < benchGroups; i += benchProtectedEvery {
		group := access.Subject{Kind: access.SubjectGroup, ID: fmt.Sprintf("grp-%04d", i)}
		grants = append(grants, protectionGrant(fmt.Sprintf("grt-bench-op-%04d", i), group,
			access.RoleInstallationOperator, server))
	}

	for i := benchActAsEvery / 2; i < benchGroups; i += benchActAsEvery {
		group := access.Subject{Kind: access.SubjectGroup, ID: fmt.Sprintf("grp-%04d", i)}
		grants = append(grants, protectionGrant(fmt.Sprintf("grt-bench-act-%04d", i), group,
			access.RoleTokenManager, identity.PrincipalObject(svc.ID)))
	}

	for i := range grants {
		if err := s.CreateGrant(ctx, grants[i], none, entry(), nil); err != nil &&
			!errors.Is(err, access.ErrAlreadyExists) {
			b.Fatalf("seed grant %s: %v", grants[i].ID, err)
		}
	}
}

// protectionGrant returns a grant of BenchProtectedGroups.
func protectionGrant(id string, subject access.Subject, role string, obj identity.Object) access.Grant {
	return access.Grant{
		CreatedAt: at(1), Condition: nil, Object: obj, ID: id, Role: role, CreatedBy: "admin", Subject: subject,
	}
}

// countTrue returns the number of true flags of m.
func countTrue(m map[string]bool) int {
	n := 0

	for _, v := range m {
		if v {
			n++
		}
	}

	return n
}

// readCount counts the calls of the reach ports.
type readCount struct {
	n int
}

// countingReach returns cfg with each port behind a counter of its calls.
func countingReach(cfg access.ReachConfig, reads *readCount) access.ReachConfig {
	return access.ReachConfig{
		Grants:      countingGrants{GrantLookups: cfg.Grants, reads: reads},
		Roles:       countingRoles{RoleStore: cfg.Roles, reads: reads},
		Callers:     countingCallers{next: cfg.Callers, reads: reads},
		Principals:  countingPrincipals{next: cfg.Principals, reads: reads},
		Collections: countingCollections{UnitCollections: cfg.Collections, reads: reads},
		Units:       countingUnits{Units: cfg.Units, reads: reads},
	}
}

type countingGrants struct {
	access.GrantLookups

	reads *readCount
}

func (c countingGrants) GrantsOf(ctx context.Context, subject access.Subject) ([]access.Grant, error) {
	c.reads.n++

	return c.GrantLookups.GrantsOf(ctx, subject)
}

func (c countingGrants) GrantsOfSubjects(ctx context.Context, subjects []access.Subject) ([]access.Grant, error) {
	c.reads.n++

	return c.GrantLookups.GrantsOfSubjects(ctx, subjects)
}

type countingRoles struct {
	access.RoleStore

	reads *readCount
}

func (c countingRoles) GetRoles(ctx context.Context, ids []string) ([]access.Role, error) {
	c.reads.n++

	return c.RoleStore.GetRoles(ctx, ids)
}

type countingCallers struct {
	next  access.Callers
	reads *readCount
}

func (c countingCallers) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	c.reads.n++

	return c.next.SubjectsOf(ctx, subject)
}

type countingPrincipals struct {
	next  access.PrincipalNamespaces
	reads *readCount
}

func (c countingPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	c.reads.n++

	return c.next.GetPrincipal(ctx, id)
}

func (c countingPrincipals) ListNamespacePrincipals(
	ctx context.Context, ns namespace.Name, q principal.Query,
) ([]principal.Principal, int64, error) {
	c.reads.n++

	return c.next.ListNamespacePrincipals(ctx, ns, q)
}

type countingCollections struct {
	access.UnitCollections

	reads *readCount
}

func (c countingCollections) CollectionID(ctx context.Context, obj identity.Object) (string, bool, error) {
	c.reads.n++

	return c.UnitCollections.CollectionID(ctx, obj)
}

type countingUnits struct {
	access.Units

	reads *readCount
}

func (c countingUnits) UnitObjects(ctx context.Context, ids []string) (map[string]identity.Object, error) {
	c.reads.n++

	return c.Units.UnitObjects(ctx, ids)
}
