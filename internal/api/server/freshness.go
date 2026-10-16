package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// errReauthenticate reports a console session whose sign-in is older than
// the token freshness window of the session policy.
var errReauthenticate = errors.New("the sign-in of this console session is too old for this change")

// reauthenticateError is the refusal of a token change from a console
// session whose sign-in is older than window. URL starts a sign-in that
// asks the identity provider for a new authentication. The console adds
// the query parameter return.
type reauthenticateError struct {
	url    string
	window time.Duration
}

func (e reauthenticateError) Error() string {
	return fmt.Sprintf("the sign-in of this console session is older than %s, so sign in again at the identity "+
		"provider before this change", e.window)
}

// Unwrap returns errReauthenticate.
func (reauthenticateError) Unwrap() error { return errReauthenticate }

// errNoFreshnessCheck reports a token change from a console session that
// reached an operation with no freshness check: the route table must wrap
// each token change with serveTokenChange. It fails the request closed.
var errNoFreshnessCheck = errors.New("a token change from a console session has no freshness check")

// freshnessKey carries the freshness check of a token change.
type freshnessKey struct{}

// requireFresh runs the freshness check of a token change on a request
// with a console session. A request with an access token passes. A
// request with a session and no check in its context returns
// errNoFreshnessCheck.
func requireFresh(r *http.Request) error {
	if _, ok := sessionFromRequest(r); !ok {
		return nil
	}

	check, ok := r.Context().Value(freshnessKey{}).(func(*http.Request) error)
	if !ok {
		return errNoFreshnessCheck
	}

	return check(r)
}

// reauthenticateResponse is the error body of a token change from a
// console session whose sign-in is too old. Reauthenticate is the path
// that starts a new sign-in; the console adds the query parameter return.
//
// Contract: never remove or rename these fields.
type reauthenticateResponse struct {
	Message        string `json:"message"`
	Reauthenticate string `json:"reauthenticate"`
	Code           int    `json:"code"`
}

// freshParam reports whether the start of a sign-in asks for a new
// authentication at the identity provider (fresh=1). Another value of the
// parameter returns errFreshParam.
func freshParam(r *http.Request) (bool, error) {
	switch r.URL.Query().Get("fresh") {
	case "":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, errFreshParam
	}
}

// checkTokenFreshness refuses a token change, or a change of the session
// policy, from a console session whose sign-in is older than the token
// freshness window of the session policy. A request with an access token,
// and a window of 0, pass. With no session policy service, the window is
// session.DefaultTokenFreshness.
func (h *Handler) checkTokenFreshness(r *http.Request) error {
	caller, ok := sessionFromRequest(r)
	if !ok {
		return nil
	}

	if h.signIn == nil {
		return errNoFreshnessCheck
	}

	window := session.DefaultTokenFreshness

	if h.sessions != nil {
		policy, err := h.sessions.Policy(r.Context())
		if err != nil {
			return fmt.Errorf("read the token freshness window: %w", err)
		}

		window = policy.TokenFreshness
	}

	if window == 0 || h.signIn.sessions.Now().Sub(caller.Match.Session.AuthTime) <= window {
		return nil
	}

	return reauthenticateError{
		url:    signInStartPrefix + url.PathEscape(caller.Match.Session.Provider) + "?fresh=1",
		window: window,
	}
}
