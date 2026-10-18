package secret_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

func TestNewItemRef(t *testing.T) {
	t.Parallel()

	scope, err := secret.NewScope("acme", "ws-one")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	cases := []struct {
		want  error
		scope secret.Scope
		kind  secret.ItemKind
		name  string
		item  string
	}{
		{name: "a state version", scope: scope, kind: secret.ItemKindState, item: "sv-1", want: nil},
		{name: "a plan", scope: scope, kind: secret.ItemKindPlan, item: "pln-1", want: nil},
		{name: "a job log", scope: scope, kind: secret.ItemKindJobLog, item: "job-1", want: nil},
		{name: "an unknown kind", scope: scope, kind: "events", item: "sv-1", want: secret.ErrInvalidItemRef},
		{
			name:  "an empty item",
			scope: scope,
			kind:  secret.ItemKindState,
			item:  "",
			want:  secret.ErrInvalidItemRef,
		},
		{
			name:  "an item with a slash",
			scope: scope,
			kind:  secret.ItemKindState,
			item:  "a/b",
			want:  secret.ErrInvalidItemRef,
		},
		{
			name:  "a long item",
			scope: scope,
			kind:  secret.ItemKindState,
			item:  strings.Repeat("a", 101),
			want:  secret.ErrInvalidItemRef,
		},
		{
			name:  "a bad scope",
			scope: secret.Scope{Namespace: "acme", ID: ""},
			kind:  secret.ItemKindState,
			item:  "sv-1",
			want:  secret.ErrInvalidScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := secret.NewItemRef(tc.scope, tc.kind, tc.item); !errors.Is(err, tc.want) {
				t.Errorf("NewItemRef err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestItemKeyID(t *testing.T) {
	t.Parallel()

	id := secret.NewItemKeyID()

	parsed, err := secret.ParseItemKeyID(string(id))
	if err != nil || parsed != id {
		t.Fatalf("parse %q = %q, %v", id, parsed, err)
	}

	if got, want := id.Object().String(), "item-keys/"+string(id); got != want {
		t.Errorf("object = %q, want %q", got, want)
	}

	for _, bad := range []string{"", "ik-", "key-1", "ik-A", "ik-a/b"} {
		if _, err := secret.ParseItemKeyID(bad); !errors.Is(err, secret.ErrInvalidItemRef) {
			t.Errorf("parse %q err = %v, want ErrInvalidItemRef", bad, err)
		}
	}
}
