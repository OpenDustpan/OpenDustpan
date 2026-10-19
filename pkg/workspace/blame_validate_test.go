package workspace_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func TestItemBlameValidate(t *testing.T) {
	t.Parallel()

	row := func(addr string, kind workspace.ResourceChangeKind) workspace.ResourceChange {
		return workspace.ResourceChange{
			At: time.Time{}, Workspace: "ws-1", StateVersion: "sv-1", Address: addr, Kind: kind, Ordinal: 1,
			ChangedPaths: 1, By: history.Actor{Subject: "", TokenID: "", JobID: ""},
			Restored: false, Forced: false, LineageChanged: false,
		}
	}
	blame := func(changes ...workspace.ResourceChange) workspace.ItemBlame {
		return workspace.ItemBlame{
			Workspace: "ws-1", StateVersion: "sv-1", Changes: changes, Ordinal: 1, Opaque: false, LineageChanged: false,
		}
	}

	negative := row("a", workspace.ResourceCreated)
	negative.ChangedPaths = -1

	other := row("a", workspace.ResourceCreated)
	other.Ordinal = 2

	opaque := blame(row("a", workspace.ResourceCreated))
	opaque.Opaque = true

	cases := []struct {
		name    string
		b       workspace.ItemBlame
		invalid bool
	}{
		{
			name:    "a readable blame",
			b:       blame(row("a", workspace.ResourceCreated), row("b", workspace.ResourceDeleted)),
			invalid: false,
		},
		{name: "a blame with no row", b: blame(), invalid: false},
		{name: "a negative count", b: blame(negative), invalid: true},
		{name: "a row of another ordinal", b: blame(other), invalid: true},
		{name: "an unknown kind", b: blame(row("a", "moved")), invalid: true},
		{
			name:    "an address twice",
			b:       blame(row("a", workspace.ResourceCreated), row("a", workspace.ResourceUpdated)),
			invalid: true,
		},
		{name: "an opaque blame with a row", b: opaque, invalid: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.b.Validate()
			if tc.invalid != errors.Is(err, workspace.ErrInvalidBlame) || (!tc.invalid && err != nil) {
				t.Errorf("Validate() = %v, want invalid = %v", err, tc.invalid)
			}
		})
	}
}
