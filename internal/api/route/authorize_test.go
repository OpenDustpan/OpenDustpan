package route_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	errBackend = errors.New("the authorizer is not reachable")
	errUnknown = errors.New("the object is unknown")
)

// checkAnswer checks the answer of AuthorizeObject against want. A nil want
// needs no error. A want other than the not-found error must not answer it.
func checkAnswer(t *testing.T, err, want error) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}

	if !errors.Is(want, errUnknown) && errors.Is(err, errUnknown) {
		t.Fatalf("err = %v, answers not found; want %v", err, want)
	}
}

// TestAuthorizeObject checks the order of the checks of AuthorizeObject and
// the answer of each refusal: a refused read is the not-found error, a refused
// action is its denial, and an authorizer error stays an error of its own.
func TestAuthorizeObject(t *testing.T) {
	t.Parallel()

	var (
		nsObject   = identity.NamespaceObject(namespace.Name("acme"))
		nsRead     = identity.ActionNamespaceRead
		unitRead   = identity.ActionUnitRead
		unitWrite  = identity.ActionUnitWrite
		reads      = []identity.Action{unitRead}
		writes     = []identity.Action{unitWrite}
		nsReadOnly = map[identity.Action]bool{nsRead: true}
		readAndObj = map[identity.Action]bool{nsRead: true, unitRead: true}
		allowAll   = map[identity.Action]bool{nsRead: true, unitRead: true, unitWrite: true}
	)

	cases := []struct {
		name      string
		allow     map[identity.Action]bool
		authErr   error
		reads     []identity.Action
		actions   []identity.Action
		wantAsked []identity.Action
		wantErr   error
	}{
		{
			name:      "allowed: read and action",
			allow:     allowAll,
			authErr:   nil,
			reads:     reads,
			actions:   writes,
			wantAsked: []identity.Action{nsRead, unitRead, unitWrite},
			wantErr:   nil,
		},
		{
			name:      "no namespace read answers not found",
			allow:     map[identity.Action]bool{},
			authErr:   nil,
			reads:     reads,
			actions:   writes,
			wantAsked: []identity.Action{nsRead},
			wantErr:   errUnknown,
		},
		{
			name:      "no read on the object answers not found",
			allow:     nsReadOnly,
			authErr:   nil,
			reads:     reads,
			actions:   writes,
			wantAsked: []identity.Action{nsRead, unitRead},
			wantErr:   errUnknown,
		},
		{
			name:      "read without the action answers a denial",
			allow:     readAndObj,
			authErr:   nil,
			reads:     reads,
			actions:   writes,
			wantAsked: []identity.Action{nsRead, unitRead, unitWrite},
			wantErr:   identity.ErrDenied,
		},
		{
			name:      "authorizer error stays its own error",
			allow:     map[identity.Action]bool{},
			authErr:   errBackend,
			reads:     reads,
			actions:   writes,
			wantAsked: []identity.Action{nsRead},
			wantErr:   errBackend,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				seen []string
				p    identity.Principal
			)

			authz := newAuthorizer(tc.allow, tc.authErr)
			g := guard(t, &seen, authz)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
			checks := route.Checks{
				Namespace: nsObject,
				Reads:     tc.reads,
				Actions:   tc.actions,
				NotFound:  errUnknown,
			}

			err := g.AuthorizeObject(t.Context(), r, p, nsObject, checks)

			if !slices.Equal(*authz.asked, tc.wantAsked) {
				t.Fatalf("asked = %v, want %v", *authz.asked, tc.wantAsked)
			}

			checkAnswer(t, err, tc.wantErr)
		})
	}
}

// TestAuthorizeObjectNoActionAllowed checks that a route with no reads and no
// actions passes once the namespace read is allowed, and asks nothing more.
func TestAuthorizeObjectNoActionAllowed(t *testing.T) {
	t.Parallel()

	var p identity.Principal

	nsObject := identity.NamespaceObject(namespace.Name("acme"))
	authz := newAuthorizer(map[identity.Action]bool{identity.ActionNamespaceRead: true}, nil)
	g := guard(t, new([]string), authz)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	checks := route.Checks{Namespace: nsObject, Reads: nil, Actions: nil, NotFound: errUnknown}

	err := g.AuthorizeObject(t.Context(), r, p, nsObject, checks)
	checkAnswer(t, err, nil)

	if want := []identity.Action{identity.ActionNamespaceRead}; !slices.Equal(*authz.asked, want) {
		t.Fatalf("asked = %v, want %v", *authz.asked, want)
	}
}
