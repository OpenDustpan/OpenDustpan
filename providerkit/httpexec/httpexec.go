// Package httpexec is the HTTP execution port of the HTTP engine: one request
// out, one reply back. The engine builds each request from the declaration,
// and decides retries, pages and the mapping of the reply. An adapter only
// sends.
//
// A request that the engine builds holds no credential. The adapter asks its
// Authorizer for the credential just before it sends, so a log or a test
// record of a request never holds one.
package httpexec

import (
	"context"
	"errors"
	"net/http"
)

// ErrTooLarge reports a reply body larger than the limit of the caller. The
// engine does not retry it.
var ErrTooLarge = errors.New("reply body too large")

// Request is one HTTP request, without credentials.
type Request struct {
	// Method is the HTTP method.
	Method string
	// URL is the absolute URL, with the query.
	URL string
	// Header holds the headers that the declaration sets, and Content-Type
	// and Accept. It holds no credential.
	Header http.Header
	// Body is the request body, or nil for none.
	Body []byte
}

// Reply is one HTTP reply.
type Reply struct {
	// Status is the HTTP status code.
	Status int
	// Header holds the reply headers.
	Header http.Header
	// Body is the whole reply body.
	Body []byte
}

// Caller is the port of the HTTP engine.
type Caller interface {
	// Call sends the request once and returns the reply. A caller is built
	// with the Origin of the base URL. A request to another origin is
	// ErrOtherOrigin: the caller sends nothing and does not ask its
	// Authorizer. Else it adds the credential of its Authorizer to a copy of
	// the headers and does not change req. A reply of any status is a
	// reply, not an error. It never follows a redirect: a 3xx comes back as
	// a reply, so a credential never goes to a host that the user did not
	// configure. Errors: ErrOtherOrigin, ErrTooLarge, the error of the
	// Authorizer, the error of ctx, or a network error.
	Call(ctx context.Context, req Request) (Reply, error)
}

// Authorizer adds the credential of the provider configuration to the headers
// of one request. A bearer token, a header, basic authentication and the
// OAuth 2.0 client credentials grant are each an Authorizer. The client
// credentials one gets a token from the token endpoint and keeps it until it
// expires.
type Authorizer interface {
	// Authorize sets the credential headers in h. Errors: a failure to get
	// a token, wrapped.
	Authorize(ctx context.Context, h http.Header) error
}
