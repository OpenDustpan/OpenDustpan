//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// starterID is the ID of the person that SeedStarter makes.
const starterID = "e2e-starter"

// errNoStarterRows reports a store that keeps no principals or grants.
var errNoStarterRows = errors.New("the store keeps no principals or grants")

// SeedStarter makes the person record of the principal that the access token
// of the env authenticates as, and grants role on Namespace to that person.
// The credentials path of a launcher session needs both: the record of the
// starter of a job, and a grant that still lets the starter start it. The
// namespace must exist. It is a direct store write, so call it before the
// runner claims a phase.
func (e *Env) SeedStarter(ctx context.Context, tb testing.TB, role string) {
	tb.Helper()

	e.withStore(ctx, tb, func(st namespace.Store) error { return seedStarter(ctx, st, role) })
}

// seedStarter stores the person of the token of the env and its grant.
func seedStarter(ctx context.Context, st namespace.Store, role string) error {
	rows, ok := st.(interface {
		principal.Store
		access.Store
	})
	if !ok {
		return errNoStarterRows
	}

	at := time.Now().UTC().Truncate(time.Millisecond)

	person := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: starterID,
		Subject: Subject, DisplayName: Subject, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if err := rows.CreatePrincipal(ctx, person, freshAdminEntry(at, principal.Ref(person.ID))); err != nil {
		return fmt.Errorf("create the starter: %w", err)
	}

	ns, err := st.GetNamespace(ctx, namespace.Name(Namespace))
	if err != nil {
		return fmt.Errorf("read the namespace: %w", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.NamespaceObject(Namespace),
		ID: "grt-e2e-starter", Role: role, CreatedBy: freshAdminActor,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: person.ID},
	}

	entry := freshAdminEntry(at, access.GrantRef(grant))
	entry.Namespace = ns.Name

	fence := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}
	if err := rows.CreateGrant(ctx, grant, fence, entry, nil); err != nil {
		return fmt.Errorf("grant %s: %w", role, err)
	}

	return nil
}
