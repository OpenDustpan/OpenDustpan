package inventory_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errCoveringDown = errors.New("manual lock store down")

// coveringLocks is a manual lock store that answers one covering lock, or an
// error, for every unit.
type coveringLocks struct {
	*memLocks

	lock manuallock.Lock
	err  error
}

func (c coveringLocks) CoveringLock(context.Context, collection.UnitRef) (manuallock.Lock, error) {
	if c.err != nil {
		return manuallock.Lock{}, c.err
	}

	return c.lock, nil
}

// lockOf returns a manual lock of alice on target.
func lockOf(target manuallock.Target) manuallock.Lock {
	return manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: target, Holder: "alice", Reason: "change window closed",
	}
}

// lockService returns a factory for the manual lock service over store.
func lockService(
	t *testing.T,
	store coveringLocks,
	authz identity.Authorizer,
) func(namespace.Store) *manuallock.Service {
	t.Helper()

	return func(ns namespace.Store) *manuallock.Service {
		svc, err := manuallock.NewService(manuallock.Config{
			Store: store, Namespaces: ns, Authorizer: authz, Phases: nil, Now: nil,
		})
		if err != nil {
			t.Fatalf("manual lock service: %v", err)
		}

		return svc
	}
}

// moveCase is one case of the move out of a collection: the manual lock store,
// whether the caller holds unit:manage, the expected status, a text that the
// answer must hold, and the text that the audit version must hold.
type moveCase struct {
	name       string
	store      coveringLocks
	canManage  bool
	wantStatus int
	wantText   string
	wantAudit  string
}

// checkMoveCase checks the answer, the unit and the audit entry of one case.
func checkMoveCase(t *testing.T, tc moveCase, f moveFixture, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
	}

	if tc.wantText != "" && !strings.Contains(rec.Body.String(), tc.wantText) {
		t.Errorf("body = %s, want it to name %q", rec.Body.String(), tc.wantText)
	}

	if moved := f.store.members[f.unitRef] == "new"; moved != (tc.wantStatus == http.StatusOK) {
		t.Errorf("unit moved = %v after status %d", moved, rec.Code)
	}

	if tc.wantStatus != http.StatusOK {
		if len(f.store.entries) != 0 {
			t.Errorf("audit entries after a refused move = %d, want 0", len(f.store.entries))
		}

		return
	}

	if len(f.store.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.store.entries))
	}

	version := f.store.entries[0].Version
	if got := strings.Contains(version, "manual-lock"); got != (tc.wantAudit != "") {
		t.Errorf("audit version = %q, want manual-lock named = %v", version, tc.wantAudit != "")
	}

	if !strings.Contains(version, tc.wantAudit) {
		t.Errorf("audit version = %q, want it to hold %q", version, tc.wantAudit)
	}
}

// TestMoveOutOfALockedCollection proves a unit moves out of a collection that
// has a manual lock only for a caller with unit:manage on the unit, that the
// refusal names the lock, and that the audit entry of an allowed move names it.
// A lock on the unit, on another collection or on the namespace does not stop
// the move, and a lock that cannot be read does.
func TestMoveOutOfALockedCollection(t *testing.T) {
	t.Parallel()

	old := manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "old"})
	other := manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "new"})
	unit := manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"})
	space := manuallock.NamespaceTarget("acme")

	covered := func(target manuallock.Target) coveringLocks {
		return coveringLocks{memLocks: nil, lock: lockOf(target), err: nil}
	}
	failing := func(err error) coveringLocks {
		none := manuallock.UnitTarget(collection.UnitRef{Namespace: "", Tool: "", Name: ""})

		return coveringLocks{
			memLocks: nil,
			lock:     manuallock.Lock{Since: time.Time{}, Target: none, Holder: "", Reason: ""},
			err:      err,
		}
	}

	conflict := "is in collection acme/old, which has a manual lock by alice"
	cases := []moveCase{
		{"locked collection, caller without unit:manage", covered(old), false, http.StatusConflict, conflict, ""},
		{"locked collection, caller with unit:manage", covered(old), true, http.StatusOK, "", "manual-lock=old"},
		{"lock on the unit", covered(unit), false, http.StatusOK, "", ""},
		{"lock on the other collection", covered(other), false, http.StatusOK, "", ""},
		{"lock on the namespace", covered(space), false, http.StatusOK, "", ""},
		{"no lock", failing(manuallock.ErrNotLocked), false, http.StatusOK, "", ""},
		{"lock cannot be read", failing(errCoveringDown), true, http.StatusInternalServerError, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.store.memLocks = newMemLocks()

			var authz identity.Authorizer = objectDenier{action: identity.ActionUnitManage, object: "pulumi:acme/web/prod"}
			if tc.canManage {
				authz = objectDenier{action: "", object: ""}
			}

			f := newMoveFixtureFull(t, authz, allowMoves{}, nil, lockService(t, tc.store, authz))

			checkMoveCase(t, tc, f, f.move(t, "new"))
		})
	}
}
