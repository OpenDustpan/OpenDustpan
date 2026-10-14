package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// scopeFixture is a SQLite store with the namespaces acme and other, the
// stack acme/web/prod, and an access.Authorizer over it.
type scopeFixture struct {
	store *sqlite.Store
	authz *access.Authorizer
	unit  identity.Object
}

// scopeAt is the time of every seeded record.
func scopeAt() time.Time {
	return time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
}

func newScopeFixture(t *testing.T) scopeFixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, ns := range []namespace.Name{"acme", "other"} {
		if _, createErr := namespaces.Create(t.Context(), ns); createErr != nil {
			t.Fatalf("create namespace %s: %v", ns, createErr)
		}
	}

	rec, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	ref := stack.Ref{Namespace: "acme", Project: "web", Stack: "prod"}
	s := stack.Stack{
		CreatedAt: scopeAt(), UpdatedAt: scopeAt(), Tags: map[string]string{}, Ref: ref, ID: "stk-scope", Version: 0,
	}

	if createErr := st.CreateStack(t.Context(), s, rec.Fence()); createErr != nil {
		t.Fatalf("create stack: %v", createErr)
	}

	unit, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{Store: st, Callers: st, Collections: nil, Units: st})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	return scopeFixture{store: st, authz: authz, unit: unit.WithUnitID(string(s.ID))}
}

// seed stores the principal "prn-<subject>" with a viewer grant on obj.
func (f scopeFixture) seed(t *testing.T, subject string, obj identity.Object) {
	t.Helper()

	prn := principal.Principal{
		CreatedAt: scopeAt(), UpdatedAt: scopeAt(), ID: "prn-" + subject, Subject: subject,
		DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal, GroupCount: 0,
	}
	change := storetest.ServerChange(scopeAt(), "principal:"+prn.ID)
	if err := f.store.CreatePrincipal(t.Context(), prn, change); err != nil {
		t.Fatalf("seed principal: %v", err)
	}

	grt := access.Grant{
		CreatedAt: scopeAt(), Condition: nil, Object: obj, ID: "grt-" + subject, Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: prn.ID},
	}
	fence, e := storetest.GrantWrite(t, f.store, obj, scopeAt(), "grant:"+grt.ID)

	if err := f.store.CreateGrant(t.Context(), grt, fence, e); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

// TestDeletedReadScope proves the read scope of the deleted-units list: a
// grant on the namespace covers every unit of that namespace and none of
// another, a grant on a unit covers its stable ID, and a caller with no grant
// reads nothing.
func TestDeletedReadScope(t *testing.T) {
	t.Parallel()

	f := newScopeFixture(t)
	f.seed(t, "nina", identity.NamespaceObject("acme"))
	f.seed(t, "uma", f.unit)

	cases := []struct {
		name    string
		subject string
		ns      namespace.Name
		wantIDs []string
		wantAll bool
	}{
		{name: "namespace grant", subject: "nina", ns: "acme", wantIDs: nil, wantAll: true},
		{name: "namespace grant elsewhere", subject: "nina", ns: "other", wantIDs: []string{}, wantAll: false},
		{name: "unit grant", subject: "uma", ns: "acme", wantIDs: []string{"stk-scope"}, wantAll: false},
		{name: "no grant", subject: "nobody", ns: "acme", wantIDs: []string{}, wantAll: false},
	}

	scope := deletedReadScope{authz: f.authz}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := identity.Principal{Subject: tc.subject, DisplayName: "", Admin: false}

			all, ids, err := scope.ReadableUnits(t.Context(), p, tc.ns)
			if err != nil {
				t.Fatalf("readable units: %v", err)
			}

			if all != tc.wantAll || (!all && !slices.Equal(ids, tc.wantIDs)) {
				t.Fatalf("readable units = %v %v, want %v %v", all, ids, tc.wantAll, tc.wantIDs)
			}
		})
	}
}

// TestDeletedReadScopeFailsClosed proves that an authorizer that lists no
// scope gives every deleted unit to an administrator and none to anyone
// else.
func TestDeletedReadScopeFailsClosed(t *testing.T) {
	t.Parallel()

	scope := deletedReadScope{authz: identity.AdminOnly{}}

	for _, admin := range []bool{true, false} {
		p := identity.Principal{Subject: "someone", DisplayName: "", Admin: admin}

		all, ids, err := scope.ReadableUnits(t.Context(), p, "acme")
		if err != nil || all != admin || len(ids) != 0 {
			t.Fatalf("admin %v: readable units = %v %v %v, want %v and no ID", admin, all, ids, err, admin)
		}
	}
}

// errBlobDelete is the failure of a blob delete in the blob purger tests.
var errBlobDelete = errors.New("blob delete failed")

// recordedPurges is an inventory.BlobPurges that records each finish.
type recordedPurges struct {
	finished []string
}

func (r *recordedPurges) ListBlobPurges(_ context.Context, _ int) ([]inventory.BlobPurge, error) {
	return nil, nil
}

func (r *recordedPurges) FinishBlobPurge(_ context.Context, tool, unitID string) error {
	r.finished = append(r.finished, tool+"/"+unitID)

	return nil
}

// TestBlobPurgerFinishesOnlyWhenEveryBlobIsGone proves that the purger
// finishes the record after a delete that removes every blob, and keeps it
// for the sweep after a delete that leaves a blob.
func TestBlobPurgerFinishesOnlyWhenEveryBlobIsGone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deleteErr error
		name      string
		want      []string
	}{
		{deleteErr: nil, name: "every blob gone", want: []string{"opentofu/ws-1"}},
		{deleteErr: errBlobDelete, name: "a blob remains", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			purges := &recordedPurges{finished: nil}
			purger := blobPurger{purges: purges, logger: slog.New(slog.DiscardHandler)}

			purger.finish(t.Context(), "opentofu", "ws-1", func(context.Context) error { return tc.deleteErr })

			if !slices.Equal(purges.finished, tc.want) {
				t.Fatalf("finished = %v, want %v", purges.finished, tc.want)
			}
		})
	}
}

// TestBlobPurgerOutlivesTheRequest proves that the blob deletes run on a
// context that the request cannot cancel, with a deadline.
func TestBlobPurgerOutlivesTheRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	purges := &recordedPurges{finished: nil}
	purger := blobPurger{purges: purges, logger: slog.New(slog.DiscardHandler)}

	purger.finish(ctx, "pulumi", "stk-1", func(c context.Context) error {
		if c.Err() != nil {
			return fmt.Errorf("blob context: %w", c.Err())
		}

		if _, ok := c.Deadline(); !ok {
			return errBlobDelete
		}

		return nil
	})

	if !slices.Equal(purges.finished, []string{"pulumi/stk-1"}) {
		t.Fatalf("finished = %v, want the purge of pulumi/stk-1", purges.finished)
	}
}
