package gate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestStateWriteFactsChecksTheChangeAgain sends a state change that is not
// valid straight to the facts builder, as if the check at the boundary of the
// evaluation had not run. The builder is a defense-in-depth layer and stands
// alone: it refuses the change and builds no fact from it.
func TestStateWriteFactsChecksTheChangeAgain(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	other, err := identity.UnitObject("opentofu", "acme", "db")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		op    OperationKind
		units []identity.Object
		state *StateChange
		want  error
	}{
		{
			"a lock with a state change", OperationLock,
			[]identity.Object{unit},
			&StateChange{Current: "sv-1", Resources: 1, Removed: nil, Unavailable: ""}, ErrInvalid,
		},
		{
			"no current version", OperationRollback,
			[]identity.Object{unit},
			&StateChange{Current: "", Resources: 1, Removed: nil, Unavailable: ""}, ErrInvalid,
		},
		{
			"an address with a newline", OperationRollback,
			[]identity.Object{unit},
			&StateChange{Current: "sv-1", Resources: 1, Removed: []string{"a\nb"}, Unavailable: ""}, ErrInvalid,
		},
		{
			"too many addresses", OperationRollback,
			[]identity.Object{unit},
			&StateChange{
				Current: "sv-1", Resources: 1, Removed: make([]string, MaxRemovedAddresses+1), Unavailable: "",
			}, ErrLimit,
		},
		{
			"an address above its limit", OperationRollback,
			[]identity.Object{unit},
			&StateChange{
				Current:     "sv-1",
				Resources:   1,
				Removed:     []string{strings.Repeat("a", MaxAddressBytes+1)},
				Unavailable: "",
			}, ErrLimit,
		},
		{
			"two units", OperationRollback,
			[]identity.Object{unit, other},
			&StateChange{Current: "sv-1", Resources: 1, Removed: nil, Unavailable: ""}, ErrInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := Change{
				Point:     PointStateWrite,
				Namespace: "acme",
				Ref:       ChangeRef{Kind: ChangeStateWrite, ID: ""},
				Subject: Subject{
					ID: "alice", Groups: nil, Kind: PrincipalPerson, Credential: CredentialAccessToken,
					AuthAt: time.Time{}, BreakGlassAccount: false, Job: nil,
				},
				Source:     SourceCLI,
				Author:     "alice",
				Operation:  tc.op,
				Units:      tc.units,
				Holders:    nil,
				PlanLock:   false,
				BreakGlass: "",
				Fence:      namespace.Fence{Namespace: "acme", FailoverVersion: 1},
				State:      tc.state,
			}

			f, doc, err := stateWriteFacts(c, unit, "", nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("stateWriteFacts() err = %v, want %v", err, tc.want)
			}

			if f.State.Available || doc != nil {
				t.Fatalf("stateWriteFacts() built facts %+v and a document from a change that is not valid", f.State)
			}
		})
	}
}
