package route_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errResolve = errors.New("resolve failed")

// recorder records the steps that ran and the error that the wrapper wrote.
type recorder struct {
	steps   []string
	written error
}

func (rec *recorder) write(_ context.Context, w http.ResponseWriter, err error) {
	rec.steps = append(rec.steps, "error")
	rec.written = err

	w.WriteHeader(http.StatusTeapot)
}

// stepsFor returns the steps of an authorized route that record each call.
// resolveErr and authorizeErr make that step fail.
func (rec *recorder) stepsFor(resolveErr, authorizeErr error) route.Steps[string] {
	return route.Steps[string]{
		Scope:   route.ScopeAuthorized,
		Actions: []identity.Action{identity.ActionNamespaceRead},
		Resolve: func(http.ResponseWriter, *http.Request) (string, error) {
			rec.steps = append(rec.steps, "resolve")

			return "target", resolveErr
		},
		Authorize: func(_ *http.Request, p identity.Principal, target string) error {
			rec.steps = append(rec.steps, "authorize:"+p.Subject+":"+target)

			return authorizeErr
		},
		Serve: func(_ http.ResponseWriter, _ *http.Request, p identity.Principal, target string) {
			rec.steps = append(rec.steps, "serve:"+p.Subject+":"+target)
		},
	}
}

// serve sends one request with the Authorization header to h.
func serve(t *testing.T, h http.Handler, header string) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	if header != "" {
		r.Header.Set("Authorization", header)
	}

	h.ServeHTTP(httptest.NewRecorder(), r)
}

// TestEnforceOrder checks the fixed order of the steps of an authorized
// route, and that the first failed step stops the request.
func TestEnforceOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		header    string
		resolve   error
		authorize error
		want      []string
	}{
		{"every step passes", "token good", nil, nil, []string{"resolve", "authorize:ana:target", "serve:ana:target"}},
		{"no sign-in reads nothing", "token bad", nil, nil, []string{"error"}},
		{"no credential reads nothing", "", nil, nil, []string{"error"}},
		{"resolve fails", "token good", errResolve, nil, []string{"resolve", "error"}},
		{
			"authorization refuses",
			"token good",
			nil,
			identity.ErrDenied,
			[]string{"resolve", "authorize:ana:target", "error"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				seen []string
				rec  recorder
			)

			h := route.Enforce(guard(&seen, newAuthorizer(nil, nil)), rec.write, rec.stepsFor(tc.resolve, tc.authorize))
			serve(t, h, tc.header)

			if !slices.Equal(rec.steps, tc.want) {
				t.Fatalf("steps = %v, want %v", rec.steps, tc.want)
			}
		})
	}
}

// TestEnforcePublic checks that a public route serves with no principal and
// the zero target, and runs no other step.
func TestEnforcePublic(t *testing.T) {
	t.Parallel()

	var (
		seen []string
		rec  recorder
	)

	steps := rec.stepsFor(nil, nil)
	steps.Scope = route.ScopePublic

	serve(t, route.Enforce(guard(&seen, newAuthorizer(nil, nil)), rec.write, steps), "token good")

	if !slices.Equal(rec.steps, []string{"serve::"}) || len(seen) != 0 {
		t.Fatalf("steps = %v, tokens = %v; want only serve with no principal and no token read", rec.steps, seen)
	}
}

// TestEnforceFailsClosed sends a request to a route that has an unknown
// scope or lacks a step. The wrapper answers through the error writer and
// runs no step, as if the route test of the API had missed the row.
func TestEnforceFailsClosed(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*route.Steps[string]){
		"no scope":         func(s *route.Steps[string]) { s.Scope = 0 },
		"unknown scope":    func(s *route.Steps[string]) { s.Scope = 99 },
		"no resolver":      func(s *route.Steps[string]) { s.Resolve = nil },
		"no authorization": func(s *route.Steps[string]) { s.Authorize = nil },
		"no handler":       func(s *route.Steps[string]) { s.Serve = nil },
		// An Authorize step that checks nothing for an empty list, as the
		// walk of a row with no action did, must not let the row serve.
		"no declared action":   func(s *route.Steps[string]) { s.Actions = nil },
		"an empty action list": func(s *route.Steps[string]) { s.Actions = []identity.Action{} },
		"public, no handler": func(s *route.Steps[string]) {
			s.Scope = route.ScopePublic
			s.Serve = nil
		},
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				seen []string
				rec  recorder
			)

			steps := rec.stepsFor(nil, nil)
			change(&steps)

			serve(t, route.Enforce(guard(&seen, newAuthorizer(nil, nil)), rec.write, steps), "token good")

			if !slices.Equal(rec.steps, []string{"error"}) || rec.written == nil || len(seen) != 0 {
				t.Fatalf("steps = %v, error = %v, tokens = %v; want only the error", rec.steps, rec.written, seen)
			}
		})
	}
}

// TestAuthorizeAllRefusesNoAction sends an empty action list straight to
// AuthorizeAll, as if Enforce and the route test had both missed the row. It
// refuses with an error and asks the authorizer nothing.
func TestAuthorizeAllRefusesNoAction(t *testing.T) {
	t.Parallel()

	for name, actions := range map[string][]identity.Action{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				seen []string
				p    identity.Principal
			)

			authz := newAuthorizer(nil, nil)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)

			err := guard(&seen, authz).AuthorizeAll(r, p, identity.NamespaceObject(namespace.Name("acme")), actions)
			if err == nil || len(*authz.asked) != 0 {
				t.Fatalf("err = %v, asked = %v; want a refusal and no question", err, *authz.asked)
			}
		})
	}
}

// TestStepsOfWalksTheActions checks that the authorization of StepsOf
// authorizes each declared action of the row on the object of the target, in
// order, and stops at the first refusal.
func TestStepsOfWalksTheActions(t *testing.T) {
	t.Parallel()

	var seen []string

	authz := newAuthorizer(map[identity.Action]bool{identity.ActionNamespaceRead: true}, nil)
	g := guard(&seen, authz)
	obj := identity.NamespaceObject(namespace.Name("acme"))

	rt := route.Route[identity.Object]{
		Method:  http.MethodGet,
		Path:    "/x",
		Scope:   route.ScopeAuthorized,
		Actions: []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead, identity.ActionUnitWrite},
		Resolve: func(http.ResponseWriter, *http.Request) (identity.Object, error) { return obj, nil },
		Serve:   func(http.ResponseWriter, *http.Request, identity.Principal, identity.Object) {},
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)

	var p identity.Principal

	err := route.StepsOf(g, rt, route.Self).Authorize(r, p, obj)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("err = %v, want the refusal of the second action", err)
	}

	want := []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead}
	if !slices.Equal(*authz.asked, want) {
		t.Fatalf("asked = %v, want %v", *authz.asked, want)
	}

	if rt.Pattern() != "GET /x" {
		t.Fatalf("pattern = %q, want GET /x", rt.Pattern())
	}
}
