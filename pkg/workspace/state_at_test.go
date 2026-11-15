package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// stateAtStates is the in-memory state store plus a StateAtReader that gives
// one fixed answer.
type stateAtStates struct {
	*memStore

	err    error
	answer workspace.StateAt
}

func (s stateAtStates) WorkspaceStateAt(context.Context, workspace.ID, time.Time) (workspace.StateAt, error) {
	return s.answer, s.err
}

// stateAtService returns a service over the store of f whose state store
// answers every state-at read with answer and err.
func stateAtService(t *testing.T, f fixture, answer workspace.StateAt, err error) *workspace.Service {
	t.Helper()

	svc, newErr := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             stateAtStates{memStore: f.store, err: err, answer: answer},
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if newErr != nil {
		t.Fatalf("service: %v", newErr)
	}

	return svc
}

// noAnswer is the zero StateAt that comes with an error.
func noAnswer() workspace.StateAt {
	return workspace.StateAt{FinalizedAt: time.Time{}, By: "", StateVersion: ""}
}

// stateAtRequest asks for the state at the time at, first page.
func stateAtRequest(at time.Time) workspace.StateAtRequest {
	return workspace.StateAtRequest{At: at, Cursor: "", Filter: "", Size: 0}
}

// TestReadStateAtWithoutReader proves that a state store without the
// state-at read fails with ErrMissingDependency.
func TestReadStateAtWithoutReader(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.svc.ReadStateAt(t.Context(), f.ws.ID, stateAtRequest(*f.now))
	if !errors.Is(err, workspace.ErrMissingDependency) {
		t.Fatalf("state at without a reader: err = %v, want ErrMissingDependency", err)
	}
}

// TestReadStateAtBeforeFirstVersion proves the text of ErrNoStateAt: a
// workspace without a state version says so, and a time before the first
// state version names its finalize time in RFC 3339 and in milliseconds. The
// error is a NoStateAtError with that time, so that an adapter reads the time
// from its field and not from the text.
func TestReadStateAtBeforeFirstVersion(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, time.October, 9, 10, 0, 0, 123000000, time.UTC)

	cases := []struct {
		first time.Time
		name  string
		want  string
	}{
		{name: "no state version", first: time.Time{}, want: "acme/e2e-workspace has no state version yet"},
		{
			name:  "before the first state version",
			first: first,
			want: fmt.Sprintf("the first state version of acme/e2e-workspace is at 2026-10-09T10:00:00Z (%d ms)",
				first.UnixMilli()),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			svc := stateAtService(t, f, noAnswer(), &workspace.NoStateAtError{First: tc.first})

			got, err := svc.ReadStateAt(t.Context(), f.ws.ID, stateAtRequest(first.Add(-time.Hour)))
			if !errors.Is(err, workspace.ErrNoStateAt) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("state at: err = %v, want ErrNoStateAt with %q", err, tc.want)
			}

			if none, ok := errors.AsType[*workspace.NoStateAtError](err); !ok || !none.First.Equal(tc.first) {
				t.Fatalf("state at: err = %#v, want a NoStateAtError with First %s", err, tc.first)
			}

			if got.Version.ID != "" || got.State.StateVersion != "" || got.Opaque {
				t.Fatalf("state at = %+v with an error, want the zero value", got)
			}
		})
	}
}

// TestReadStateAtClientEncrypted proves that a state version the client
// encrypted answers as Opaque, with its version and no resource.
func TestReadStateAtClientEncrypted(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-1", 1, envelope), envelope); err != nil {
		t.Fatalf("push encrypted state: %v", err)
	}

	cur, err := f.store.CurrentStateVersion(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current state version: %v", err)
	}

	answer := workspace.StateAt{FinalizedAt: cur.FinalizedAt, By: "alice", StateVersion: cur.ID}
	svc := stateAtService(t, f, answer, nil)

	got, err := svc.ReadStateAt(ctx, f.ws.ID, stateAtRequest(cur.FinalizedAt))
	if err != nil {
		t.Fatalf("state at of an encrypted state: %v", err)
	}

	if !got.Opaque || got.Version.ID != cur.ID || got.State != answer ||
		len(got.Page.Resources) != 0 || got.Page.NextCursor != "" {
		t.Fatalf("state at = %+v, want the opaque version %s with no resource", got, cur.ID)
	}
}

// namedStates is stateAtStates plus an inventory.HistoryReader that gives
// fixed names. Every read of the log fails with err when it is set.
type namedStates struct {
	stateAtStates

	names inventory.Names
	err   error
}

func (s namedStates) ListChanges(
	context.Context, namespace.Name, time.Time, *inventory.ChangeCursor, int,
) ([]inventory.Change, error) {
	return []inventory.Change{}, s.err
}

func (s namedStates) UnitsAt(context.Context, namespace.Name, time.Time) ([]inventory.Entry, error) {
	return []inventory.Entry{}, s.err
}

func (s namedStates) ViewAt(context.Context, namespace.Name, time.Time) (inventory.UnitsView, error) {
	return inventory.UnitsView{Units: []inventory.UnitAt{}, Purged: 0}, s.err
}

func (s namedStates) UnitNames(
	context.Context, namespace.Name, string, string, time.Time,
) (inventory.Names, error) {
	return s.names, s.err
}

// errNamesFailed is the failure of the change log in TestReadStateAtNames.
var errNamesFailed = errors.New("change log failed")

// namedFixture pushes a state to the fixture workspace and returns a service
// over a state store that gives names, with the state version of the push as
// the answer of the state-at read, and the finalize time of that version.
func namedFixture(t *testing.T, names inventory.Names, logErr error) (*workspace.Service, workspace.ID, time.Time) {
	t.Helper()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-1", 1, envelope), envelope); err != nil {
		t.Fatalf("push state: %v", err)
	}

	cur, err := f.store.CurrentStateVersion(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current state version: %v", err)
	}

	answer := workspace.StateAt{FinalizedAt: cur.FinalizedAt, By: "alice", StateVersion: cur.ID}
	states := namedStates{
		stateAtStates: stateAtStates{memStore: f.store, err: nil, answer: answer},
		names:         names,
		err:           logErr,
	}

	svc, newErr := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             states,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if newErr != nil {
		t.Fatalf("service: %v", newErr)
	}

	return svc, f.ws.ID, cur.FinalizedAt
}

// TestReadStateAtNames proves that a state store that keeps the inventory
// change log gives the names of the workspace at the time and now, and that a
// failure of the log fails the read.
func TestReadStateAtNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err   error
		name  string
		names inventory.Names
	}{
		{name: "a renamed workspace", names: inventory.Names{AtTime: "network", Current: "network-2"}, err: nil},
		{name: "a failure of the log", names: inventory.Names{AtTime: "", Current: ""}, err: errNamesFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, id, finalized := namedFixture(t, tc.names, tc.err)

			got, err := svc.ReadStateAt(t.Context(), id, stateAtRequest(finalized))
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("state at: err = %v, want %v", err, tc.err)
				}

				return
			}

			if err != nil || got.Names != tc.names {
				t.Fatalf("state at names = %+v, %v, want %+v", got.Names, err, tc.names)
			}
		})
	}
}
