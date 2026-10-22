package route

import (
	"context"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Scope says what a route needs before its handler runs.
type Scope int

const (
	// ScopePublic serves without sign-in. Only routes on the public list of
	// the route test of an API may use it.
	ScopePublic Scope = iota + 1
	// ScopeAuthorized needs a signed-in principal, and the authorization of
	// the route to allow the request on the resolved target, before the
	// handler runs.
	ScopeAuthorized
)

// Route is one row of the route table of an API: the pattern, the scope, the
// actions it declares, and the steps that read the target and serve the
// request. T is the target that the resolver reads: an identity.Object, or a
// type of the API that holds one. An API whose rows need more fields embeds
// Route in its own row type.
type Route[T any] struct {
	Method  string
	Path    string
	Scope   Scope
	Actions []identity.Action
	// Resolve reads the target of the request. It runs after the sign-in, so
	// it never reads data for a caller that is not signed in.
	Resolve func(w http.ResponseWriter, r *http.Request) (T, error)
	// Serve handles the request after every check passed.
	Serve func(w http.ResponseWriter, r *http.Request, p identity.Principal, target T)
}

// Pattern returns the ServeMux pattern of the route.
func (rt Route[T]) Pattern() string {
	return rt.Method + " " + rt.Path
}

// Steps are the steps of one route that its API owns, in the order Enforce
// runs them: Resolve, Authorize, Serve.
type Steps[T any] struct {
	Scope   Scope
	Resolve func(w http.ResponseWriter, r *http.Request) (T, error)
	// Authorize checks the principal on the target at the point of use. It
	// returns the first refusal. The recorder of the Guard writes the
	// refusals to the audit log.
	Authorize func(r *http.Request, p identity.Principal, target T) error
	Serve     func(w http.ResponseWriter, r *http.Request, p identity.Principal, target T)
}

// StepsOf returns the steps of rt with the authorization that walks the
// declared actions of rt on the object that object returns for a target.
func StepsOf[T any](g Guard, rt Route[T], object func(T) identity.Object) Steps[T] {
	return Steps[T]{
		Scope:   rt.Scope,
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, target T) error {
			return g.AuthorizeAll(r, p, object(target), rt.Actions)
		},
		Serve: rt.Serve,
	}
}

// Self is the object function of a route whose target is the object.
func Self(obj identity.Object) identity.Object { return obj }

// ErrorWriter writes the answer of a refused or failed request.
type ErrorWriter func(ctx context.Context, w http.ResponseWriter, err error)

// errNoStep is the refusal of a route that lacks a step or has an unknown
// scope. It is a bug in the route table, which the route test of the API
// refuses too.
var errNoStep = errors.New("the route has no resolver, no authorization, no handler or no known scope")

// Enforce returns the handler of one route. The order of its steps is fixed
// for every API:
//
//  1. a public route serves with no principal and the zero target;
//  2. the Guard authenticates the credential, and the context gets the
//     session of a console request;
//  3. Resolve reads the target;
//  4. Authorize checks the principal on the target;
//  5. Serve handles the request.
//
// The first error stops the request, and write answers it. A route with an
// unknown scope or a missing step answers 500 and serves nothing.
func Enforce[T any](g Guard, write ErrorWriter, s Steps[T]) http.HandlerFunc {
	if s.Serve == nil {
		return refuse(write)
	}

	switch s.Scope {
	case ScopePublic:
		return func(w http.ResponseWriter, r *http.Request) {
			var (
				guest  identity.Principal
				target T
			)

			s.Serve(w, r, guest, target)
		}
	case ScopeAuthorized:
		// Defense in depth: the route test of each API refuses a row without
		// these steps too; this check stays so that a row that skips the test
		// never serves without its authorization.
		if s.Resolve == nil || s.Authorize == nil {
			return refuse(write)
		}

		return authorized(g, write, s)
	default:
		return refuse(write)
	}
}

// authorized returns the handler of an authorized route.
func authorized[T any](g Guard, write ErrorWriter, s Steps[T]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, caller, err := g.Authenticate(r)
		if err != nil {
			write(ctx, w, err)

			return
		}

		ctx = WithCaller(ctx, caller)
		r = r.WithContext(ctx)

		target, err := s.Resolve(w, r)
		if err != nil {
			write(ctx, w, err)

			return
		}

		if err := s.Authorize(r, p, target); err != nil {
			write(ctx, w, err)

			return
		}

		s.Serve(w, r, p, target)
	}
}

// refuse returns a handler that answers 500 for every request, for a route
// that cannot be enforced.
func refuse(write ErrorWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The error writer answers an error that no class knows with 500, and
		// it never shows the error text.
		write(r.Context(), w, errNoStep)
	}
}
