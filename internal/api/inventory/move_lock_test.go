package inventory_test

import (
	"context"
	"errors"
	"net/http"
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
func lockService(t *testing.T, store coveringLocks, authz identity.Authorizer) func(namespace.Store) *manuallock.Service {
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

	cases := []struct {
		name       string
		store      coveringLocks
		canManage  bool
		wantStatus int
		wantText   string
		wantAudit  string
	}{
		{
			name: "locked collection, caller without unit:manage", store: coveringLocks{lock: lockOf(old)},
			canManage: false, wantStatus: http.StatusConflict,
			wantText: "is in collection acme/old, which has a manual lock by alice",
		},
		{
			name: "locked collection, caller with unit:manage", store: coveringLocks{lock: lockOf(old)},
			canManage: true, wantStatus: http.StatusOK, wantAudit: "manual-lock=old",
		},
		{
			name: "lock on the unit", store: coveringLocks{lock: lockOf(unit)}, canManage: false,
			wantStatus: http.StatusOK, wantAudit: "",
		},
		{
			name: "lock on the other collection", store: coveringLocks{lock: lockOf(other)}, canManage: false,
			wantStatus: http.StatusOK, wantAudit: "",
		},
		{
			name: "lock on the namespace", store: coveringLocks{lock: lockOf(space)}, canManage: false,
			wantStatus: http.StatusOK, wantAudit: "",
		},
		{
			name: "no lock", store: coveringLocks{err: manuallock.ErrNotLocked}, canManage: false,
			wantStatus: http.StatusOK, wantAudit: "",
		},
		{
			name: "lock cannot be read", store: coveringLocks{err: errCoveringDown}, canManage: true,
			wantStatus: http.StatusInternalServerError,
		},
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

			rec := f.move(t, "new")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantText != "" && !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Errorf("body = %s, want it to name %q", rec.Body.String(), tc.wantText)
			}

			moved := f.store.members[f.unitRef] == "new"
			if moved != (tc.wantStatus == http.StatusOK) {
				t.Errorf("unit moved = %v after status %d", moved, rec.Code)
			}

			if tc.wantStatus != http.StatusOK {
				if len(f.store.entries) != 0 {
					t.Errorf("audit entries after a refused move = %d, want 0", len(f.store.entries))
				}

				return
			}

			if len(f.store.entries) != 1 || strings.Contains(f.store.entries[0].Version, "manual-lock") != (tc.wantAudit != "") ||
				(tc.wantAudit != "" && !strings.Contains(f.store.entries[0].Version, tc.wantAudit)) {
				t.Errorf("audit version = %q, want it to hold %q", f.store.entries[0].Version, tc.wantAudit)
			}
		})
	}
}
