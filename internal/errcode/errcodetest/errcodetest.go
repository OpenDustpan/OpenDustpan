// Package errcodetest holds the causes of a 403 and the checks that the handler
// tests of every HTTP adapter share, so that each adapter proves the same
// code, status and message for each cause.
package errcodetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Cause is one cause of a 403: the error that the adapter receives, the code
// that the answer must carry and the message that the answer has always had.
type Cause struct {
	Name string
	Err  error
	Code string
	// Message is the text of the answer. A session error answers its own text,
	// so a wrap that a test authorizer adds shows before it: Check accepts the
	// text as the end of the message for such a cause, and exactly for the
	// others.
	Message string
	// Fixed is true when the answer holds Message and nothing else.
	Fixed bool
}

// rules answers every namespace with a list that does not hold the address of
// a test request.
type rules struct{ list ipallow.List }

func (r rules) NamespaceList(context.Context, namespace.Name) (ipallow.List, error) {
	return r.list, nil
}

// Causes returns the causes of a 403, in the order of errcode.Forbidden. The
// allowlist error comes from the real gate, not from a stand-in.
func Causes(tb testing.TB) []Cause {
	tb.Helper()

	list, err := ipallow.ParseList([]string{"192.0.2.0/24"})
	if err != nil {
		tb.Fatalf("parse list: %v", err)
	}

	gate := ipallow.Gate{Next: nil, Rules: rules{list: list}, Enabled: true, IncludeBootstrap: false}

	ipErr := gate.CheckAddress(context.Background(), "acme")
	if ipErr == nil {
		tb.Fatal("the gate allowed an address that is not on the list")
	}

	return []Cause{
		{
			Name: "missing permission", Err: identity.ErrDenied, Code: errcode.AccessDenied,
			Message: "forbidden", Fixed: true,
		},
		{
			Name: "address outside the allowlist", Err: ipErr, Code: errcode.AccessIPNotAllowed,
			Message: "forbidden", Fixed: true,
		},
		{
			Name: "csrf header", Err: sessionauth.ErrCSRF, Code: errcode.SessionCSRF,
			Message: sessionauth.ErrCSRF.Error(), Fixed: false,
		},
		{
			Name: "other site", Err: sessionauth.ErrOtherSite, Code: errcode.SessionOtherSite,
			Message: sessionauth.ErrOtherSite.Error(), Fixed: false,
		},
	}
}

// Authorizer refuses every action with Err.
type Authorizer struct{ Err error }

// Authorize returns Err.
func (a Authorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return a.Err
}

// ListObjects returns Err.
func (a Authorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, a.Err
}

// ReadsThenRefuses lets the namespace read through and refuses every other
// action with Err. A route that hides an object answers 404 for a refused
// namespace read, so a test that wants a 403 lets this read pass first.
type ReadsThenRefuses struct{ Err error }

// Authorize allows identity.ActionNamespaceRead and returns Err for the rest.
func (a ReadsThenRefuses) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionNamespaceRead {
		return nil
	}

	return Authorizer(a).Authorize(ctx, p, action, obj)
}

// ListObjects returns Err.
func (a ReadsThenRefuses) ListObjects(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
) (identity.Scope, error) {
	return Authorizer(a).ListObjects(ctx, p, action)
}

// Check fails the test unless body is the 403 answer of c: the status in
// "code", the message that the adapter always sent and the machine code in
// "errorCode".
func Check(tb testing.TB, status int, body []byte, c Cause) {
	tb.Helper()

	var got struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(body, &got); err != nil {
		tb.Fatalf("%s: answer is not JSON: %v: %s", c.Name, err, body)
	}

	if status != 403 || got.Code != 403 {
		tb.Fatalf("%s: status = %d, code = %d, want 403: %s", c.Name, status, got.Code, body)
	}

	matches := strings.HasSuffix(got.Message, c.Message)
	if c.Fixed {
		matches = got.Message == c.Message
	}

	if !matches {
		tb.Fatalf("%s: message = %q, want %q", c.Name, got.Message, c.Message)
	}

	if got.ErrorCode != c.Code {
		tb.Fatalf("%s: errorCode = %q, want %q: %s", c.Name, got.ErrorCode, c.Code, body)
	}
}
