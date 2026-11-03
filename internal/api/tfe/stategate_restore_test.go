package tfe_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var errReadFailed = errors.New("blob store down")

// rollbackWorkspaces changes what the read of the rollback change and the
// restore answer, and counts the reads.
type rollbackWorkspaces struct {
	*writeCounter

	change  workspace.RollbackChange
	readErr error
	// restoreErr is the error of RestoreExpecting.
	restoreErr error
	reads      int
}

func (r *rollbackWorkspaces) ReadRollbackChange(
	context.Context, workspace.ID, workspace.StateVersionID,
) (workspace.RollbackChange, error) {
	r.reads++

	return r.change, r.readErr
}

func (r *rollbackWorkspaces) RestoreExpecting(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, by string, expect workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if r.restoreErr != nil {
		return workspace.StateVersion{}, r.restoreErr
	}

	return r.writeCounter.RestoreExpecting(ctx, id, from, by, expect)
}

func restoreRoute() guardRoute {
	return guardRoute{
		"restore", http.MethodPost, "/api/v2/state-versions/sv-test123/actions/restore", "", http.StatusOK,
		gate.OperationRollback, identity.ActionUnitWrite,
	}
}

// TestRestoreGivesTheGuardTheStateChange proves that the guard sees the facts
// that the server read from the two states, and the restore writes only if the
// current version is the one the facts came from.
func TestRestoreGivesTheGuardTheStateChange(t *testing.T) {
	t.Parallel()

	ws := &rollbackWorkspaces{
		writeCounter: newWriteCounter(),
		change: workspace.RollbackChange{
			Current:   "sv-test123",
			Resources: 4,
			Removed:   []string{"a.b", "c.d"},
			Encrypted: false,
		},
		readErr:    nil,
		restoreErr: nil,
		reads:      0,
	}
	guard := newFakeGuard(nil)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), restoreRoute())
	if rec.Code != http.StatusOK || len(guard.seen) != 1 || ws.count() != 1 {
		t.Fatalf("restore answers %d with %d checks and %d writes: %s", rec.Code, len(guard.seen), ws.count(), rec.Body)
	}

	got := guard.seen[0].State
	if got == nil || got.Current != "sv-test123" || got.Resources != 4 ||
		!slices.Equal(got.Removed, []string{"a.b", "c.d"}) {
		t.Fatalf("guard saw state change %+v, want current sv-test123, 4 resources, removed a.b and c.d", got)
	}
}

// TestRestoreRefusesAStateThatCannotBeRead proves the fail-closed layer of
// the adapter: a state that the server cannot read gives no guard call and no
// write, and the refusal has a catalog code and an audit entry. A state above
// a limit gives gate.limit, any other fault gives gate.unavailable.
func TestRestoreRefusesAStateThatCannotBeRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{"the store fails", errReadFailed, "gate.unavailable"},
		{"the state holds an entry the server cannot read", workspace.ErrInvalidState, "gate.unavailable"},
		{
			"a look-alike of the encryption envelope",
			fmt.Errorf("%w: state sv-1 has encrypted_data", workspace.ErrInvalidState),
			"gate.unavailable",
		},
		{"the state is over the size limit", workspace.ErrTooLarge, "gate.limit"},
		{"the state has too many resource instances", workspace.ErrTooManyResources, "gate.limit"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := &rollbackWorkspaces{
				writeCounter: newWriteCounter(),
				change:       workspace.RollbackChange{Current: "", Resources: 0, Removed: nil, Encrypted: false},
				readErr:      tc.err,
				restoreErr:   nil,
				reads:        0,
			}
			guard, entries := newFakeGuard(nil), newTestAudit()

			rec := serveGuarded(t, newAuditedHandler(t, ws, guard, entries), restoreRoute())
			checkRefusalDocument(t, rec, guardRefusal{
				name: "unreadable", code: tc.code, message: stategate.StateUnreadableText,
				err: &gate.RefusalError{Err: gate.ErrUnavailable, Record: "", Messages: nil},
			})

			if ws.count() != 0 || len(guard.seen) != 0 {
				t.Fatalf("%d writes and %d guard checks, want 0 and 0", ws.count(), len(guard.seen))
			}

			checkDenied(t, entries, identity.ActionUnitWrite)
		})
	}
}

// TestRestoreRefusesWhenTheCurrentVersionChanged proves the last layer: the
// state changed after the gate decision, so the restore answers 409 with the
// catalog code and one denied audit entry, and writes nothing.
func TestRestoreRefusesWhenTheCurrentVersionChanged(t *testing.T) {
	t.Parallel()

	ws := &rollbackWorkspaces{
		writeCounter: newWriteCounter(),
		change:       workspace.RollbackChange{Current: "sv-test123", Resources: 1, Removed: nil, Encrypted: false},
		readErr:      nil,
		restoreErr:   workspace.ErrConcurrentModification,
		reads:        0,
	}
	entries := newTestAudit()

	rec := serveGuarded(t, newAuditedHandler(t, ws, newFakeGuard(nil), entries), restoreRoute())
	if rec.Code != http.StatusConflict {
		t.Fatalf("restore answers %d, want 409: %s", rec.Code, rec.Body)
	}

	var doc gateErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 ||
		doc.Errors[0].Code != "workspace.changed-concurrently" {
		t.Fatalf("body %q: %v, want the code workspace.changed-concurrently", rec.Body, err)
	}

	if ws.count() != 0 {
		t.Fatalf("%d writes, want 0", ws.count())
	}

	checkDenied(t, entries, identity.ActionUnitWrite)
}

// TestRestoreWithGatesOffReadsNoState proves that with the gates off no gate
// reads a fact, so the server reads no state and restores as before.
func TestRestoreWithGatesOffReadsNoState(t *testing.T) {
	t.Parallel()

	ws := &rollbackWorkspaces{
		writeCounter: newWriteCounter(),
		change:       workspace.RollbackChange{Current: "", Resources: 0, Removed: nil, Encrypted: false},
		readErr:      errReadFailed,
		restoreErr:   nil,
		reads:        0,
	}

	rec := serveGuarded(t, newGuardedHandler(t, ws, stategate.Off("the test keeps no gates")), restoreRoute())
	if rec.Code != http.StatusOK || ws.reads != 0 || ws.count() != 1 || strings.Contains(rec.Body.String(), "errors") {
		t.Fatalf("restore answers %d with %d reads and %d writes: %s", rec.Code, ws.reads, ws.count(), rec.Body)
	}
}

// TestRestoreOfAnEncryptedStateGoesOnWithTheReason proves that a state the
// client encrypted does not refuse the restore: the guard sees a state change
// with the reason and the current version, and the restore goes on.
func TestRestoreOfAnEncryptedStateGoesOnWithTheReason(t *testing.T) {
	t.Parallel()

	ws := &rollbackWorkspaces{
		writeCounter: newWriteCounter(),
		change:       workspace.RollbackChange{Current: "sv-test123", Resources: 0, Removed: nil, Encrypted: true},
		readErr:      nil, restoreErr: nil, reads: 0,
	}
	guard := newFakeGuard(nil)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), restoreRoute())
	if rec.Code != http.StatusOK || len(guard.seen) != 1 || ws.count() != 1 {
		t.Fatalf("restore answers %d with %d checks and %d writes: %s", rec.Code, len(guard.seen), ws.count(), rec.Body)
	}

	got := guard.seen[0].State
	if got == nil || got.Unavailable != "state is client-encrypted" || got.Current != "sv-test123" ||
		got.Resources != 0 || len(got.Removed) != 0 {
		t.Fatalf("guard saw state change %+v, want the reason, the current version and no resource data", got)
	}
}
