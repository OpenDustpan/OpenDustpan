package secret_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

func TestNewContentRef(t *testing.T) {
	t.Parallel()

	scope, err := secret.NewScope("acme", "ws-one")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	cases := []struct {
		want  error
		scope secret.Scope
		kind  secret.ContentKind
		name  string
		item  string
	}{
		{name: "a state version", scope: scope, kind: secret.ContentKindState, item: "sv-1", want: nil},
		{name: "a plan", scope: scope, kind: secret.ContentKindPlan, item: "pln-1", want: nil},
		{name: "a job log", scope: scope, kind: secret.ContentKindJobLog, item: "job-1", want: nil},
		{name: "an unknown kind", scope: scope, kind: "events", item: "sv-1", want: secret.ErrInvalidContentRef},
		{
			name:  "an empty item",
			scope: scope,
			kind:  secret.ContentKindState,
			item:  "",
			want:  secret.ErrInvalidContentRef,
		},
		{
			name:  "an item with a slash",
			scope: scope,
			kind:  secret.ContentKindState,
			item:  "a/b",
			want:  secret.ErrInvalidContentRef,
		},
		{
			name:  "a long item",
			scope: scope,
			kind:  secret.ContentKindState,
			item:  strings.Repeat("a", 101),
			want:  secret.ErrInvalidContentRef,
		},
		{
			name:  "a bad scope",
			scope: secret.Scope{Namespace: "acme", ID: ""},
			kind:  secret.ContentKindState,
			item:  "sv-1",
			want:  secret.ErrInvalidScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := secret.NewContentRef(tc.scope, tc.kind, tc.item); !errors.Is(err, tc.want) {
				t.Errorf("NewContentRef err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestContentKeyID(t *testing.T) {
	t.Parallel()

	id := secret.NewContentKeyID()

	parsed, err := secret.ParseContentKeyID(string(id))
	if err != nil || parsed != id {
		t.Fatalf("parse %q = %q, %v", id, parsed, err)
	}

	if got, want := id.Object().String(), "content-keys/"+string(id); got != want {
		t.Errorf("object = %q, want %q", got, want)
	}

	for _, bad := range []string{"", "ck-", "key-1", "ck-A", "ck-a/b"} {
		if _, err := secret.ParseContentKeyID(bad); !errors.Is(err, secret.ErrInvalidContentRef) {
			t.Errorf("parse %q err = %v, want ErrInvalidContentRef", bad, err)
		}
	}
}
