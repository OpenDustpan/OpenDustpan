package server

// The public routes of the passkey sign-in. A person signs in with a
// discoverable passkey and user verification, with no user name and no
// password.
//
//   - POST /api/dustpan/v1/sign-in/passkey/begin starts a sign-in ceremony and
//     answers the request options. The options name no person.
//   - POST /api/dustpan/v1/sign-in/passkey/finish ends the ceremony with the
//     response of the authenticator, checks it, and answers 204 with the
//     session cookie. A break-glass account also sends a reason.
//
// Both routes are public: the person has no session yet. Four layers stop the
// abuse of them, and none relies on another:
//
//   - the source address has a budget of begins, because each begin writes a
//     challenge row;
//   - the source address has a budget of failed responses;
//   - the service of the passkeys checks the response against the relying
//     party ID and the origin of the public URL of the server, never against
//     a header of the request;
//   - the sign-in service checks the person, the state of the principal and
//     the user verification again, and starts the session.
//
// A failure of any check answers one text, so that the answer tells nothing
// about a passkey or a person. Every refusal writes an audit entry: the
// service writes the entry of a refusal that it makes, and this adapter writes
// the entry of a refusal that it makes before the service runs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/session"
)

// Paths and names of the passkey sign-in.
const (
	signInWebauthnBeginPath  = "/api/dustpan/v1/sign-in/passkey/begin"
	signInWebauthnFinishPath = "/api/dustpan/v1/sign-in/passkey/finish"
	signInWebauthnAuditActor = "sign-in:passkey"
	// passkeySignInRefused is the audit action of a refusal that this adapter
	// makes before the service of the passkeys runs.
	passkeySignInRefused = "passkey.sign-in-refused"
	passkeySignInObject  = "passkey:unknown"
	passkeySignInRoutes  = 2
)

// PasskeySignInService is the part of the service of the passkeys that the
// passkey sign-in uses. *mfasvc.Service meets it.
type PasskeySignInService interface {
	// BeginAssertion starts an assertion ceremony and returns the JSON of the
	// request options.
	BeginAssertion(ctx context.Context, a mfasvc.Actor, k mfa.Ceremony, b mfasvc.Binding) ([]byte, error)
	// FinishSignIn ends the sign-in ceremony of the response.
	FinishSignIn(ctx context.Context, a mfasvc.Actor, response []byte) (mfasvc.SignedIn, error)
}

// passkeySignInRequest is the body of the end of a passkey sign-in. Reason is
// the text that a person of a break-glass account types.
type passkeySignInRequest struct {
	Response passkeyJSON `json:"response"`
	Reason   string      `json:"reason"`
}

// passkeyRequestOptions is the answer of the begin of a passkey sign-in: the
// options that a browser gives to navigator.credentials.get. The handler
// writes the bytes of the relying party, and this type only describes them.
type passkeyRequestOptions struct {
	PublicKey passkeyJSON `json:"publicKey"`
}

// The errors of the passkey sign-in that no domain sentinel carries.
var (
	errPasskeySignInFailed = &codedError{
		status: http.StatusUnauthorized, code: errcode.MFASignInFailed, args: nil,
		message: "the passkey sign-in failed: start the sign-in again",
	}
	errBreakGlassReason = &codedError{
		status: http.StatusBadRequest, code: errcode.MFABreakGlassReasonRequired, args: nil,
		message: "a break-glass sign-in needs a reason: start the sign-in again and type the reason",
	}
	errPasskeySignInSourceLimit = &codedError{
		status: http.StatusTooManyRequests, code: errcode.MFASignInSourceLimit, args: nil,
		message: "this address holds the most open sign-ins: wait 5 minutes and try again",
	}
	errPasskeySignInUnavailable = &codedError{
		status: http.StatusServiceUnavailable, code: errcode.MFAUnavailable, args: nil,
		message: "the passkeys cannot answer: try again later",
	}
)

// passkeySignInRoutes returns the rows of the passkey sign-in. The routes need
// the service of the passkeys, the console sign-in, and a budget for the begins
// and a budget for the failures of the source address: with either budget
// missing, they are not mounted, so that no route serves a ceremony with no
// limit.
func (h *Handler) passkeySignInRoutes() []Route {
	if h.passkeys == nil || h.signIn == nil || h.signIn.passkeySignIn == nil ||
		h.signIn.passkeyBegins == nil || h.signIn.passkeyFailures == nil {
		return nil
	}

	routes := make([]Route, 0, passkeySignInRoutes)

	return append(routes,
		Route{
			Method: http.MethodPost, Path: signInWebauthnBeginPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.beginPasskeySignIn,
		},
		Route{
			Method: http.MethodPost, Path: signInWebauthnFinishPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.finishPasskeySignIn,
		},
	)
}

// admitPasskeyFinish waits or refuses as the budget of the failures of the
// address of the request says. A handler with a missing budget refuses: it
// fails closed.
//
// Defense in depth: passkeySignInRoutes mounts no route without the budgets.
// This check stays so that a change to the route table never serves a
// ceremony with no limit.
func (h *Handler) admitPasskeyFinish(ctx context.Context) error {
	if h.signIn == nil || h.signIn.passkeyFailures == nil {
		return errPasskeySignInUnavailable
	}

	if err := h.signIn.passkeyFailures.Admit(ctx); err != nil {
		return fmt.Errorf("passkey failure budget: %w", err)
	}

	return nil
}

// admitPasskeyStart is admitPasskeyFinish for the start of a ceremony. It
// also checks the budget of the starts. The budget of the failures applies to
// the start too, so that an address that its failures refuse starts no
// ceremony.
func (h *Handler) admitPasskeyStart(ctx context.Context) error {
	if err := h.admitPasskeyFinish(ctx); err != nil {
		return err
	}

	// Defense in depth: passkeySignInRoutes mounts no route without this
	// budget. This check stays so that a change to the route table never
	// serves a start with no limit.
	if h.signIn.passkeyBegins == nil {
		return errPasskeySignInUnavailable
	}

	if err := h.signIn.passkeyBegins.Admit(ctx); err != nil {
		return fmt.Errorf("passkey start budget: %w", err)
	}

	return nil
}

// refusePasskeySignIn writes the audit entry of a refusal that the adapter
// makes, and answers err.
func (h *Handler) refusePasskeySignIn(w http.ResponseWriter, r *http.Request, cause string, err error) {
	e := signInEntry(r, signInWebauthnAuditActor, passkeySignInRefused)
	e.At = h.now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindSecurity
	e.Outcome = audit.OutcomeDenied
	e.Object = passkeySignInObject
	e.Changes = []audit.Change{{Key: "cause", Old: nil, New: cause, Layer: "", TakesEffect: ""}}

	if _, appendErr := h.audit.AppendServer(r.Context(), e); appendErr != nil {
		h.failAudit(r.Context())
	}

	h.writeError(r.Context(), w, err)
}

// beginPasskeySignIn starts a sign-in ceremony and answers 200 with the
// request options. Each begin spends one unit of the begin budget of its
// address, whatever its result: each begin writes a challenge row.
func (h *Handler) beginPasskeySignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noReferrer(w)

	if h.signIn == nil || h.signIn.passkeySignIn == nil {
		h.writeError(ctx, w, errPasskeySignInUnavailable)

		return
	}

	if err := h.admitPasskeyStart(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.signIn.passkeyBegins.Fail(ctx)

	actor := mfasvc.Actor{
		Entry:       signInEntry(r, signInWebauthnAuditActor, session.ActionSignIn),
		PrincipalID: "",
		SessionID:   "",
	}

	options, err := h.signIn.passkeySignIn.BeginAssertion(ctx, actor, mfa.CeremonySignIn, mfasvc.Binding{
		PersonID: "", PendingID: "",
	})
	if err != nil {
		h.writeError(ctx, w, passkeySignInError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, json.RawMessage(options))
}

// finishPasskeySignIn ends a sign-in ceremony. It answers 204 with the session
// cookie. Each failed response spends one unit of the failure budget of its
// address and answers 401 with one text.
func (h *Handler) finishPasskeySignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noReferrer(w)

	if h.signIn == nil || h.signIn.passkeySignIn == nil || h.signIn.service == nil {
		h.writeError(ctx, w, errPasskeySignInUnavailable)

		return
	}

	if err := h.admitPasskeyFinish(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req passkeySignInRequest
	if err := textbody.Decode(w, r, maxPasskeyBody, &req); err != nil {
		h.signIn.passkeyFailures.Fail(ctx)
		h.refusePasskeySignIn(w, r, "request body not valid", passkeyBodyError(err))

		return
	}

	entry := signInEntry(r, signInWebauthnAuditActor, session.ActionSignIn)

	signed, err := h.signIn.passkeySignIn.FinishSignIn(
		ctx, mfasvc.Actor{Entry: entry, PrincipalID: "", SessionID: ""}, req.Response,
	)
	if err != nil {
		if !errors.Is(err, mfa.ErrUnavailable) {
			h.signIn.passkeyFailures.Fail(ctx)
		}

		h.writeError(ctx, w, passkeySignInError(err))

		return
	}

	h.completePasskey(w, r, &entry, signed, req.Reason)
}

// completePasskey gives the passkey sign-in in to the sign-in service and
// answers. The person of the passkey is the principal of the Assertion, never
// a value of the request.
func (h *Handler) completePasskey(
	w http.ResponseWriter, r *http.Request, entry *audit.Entry, signed mfasvc.SignedIn, reason string,
) {
	ctx := r.Context()

	person := signed.Passkey.PersonID
	at := h.signIn.sessions.Now()

	res, err := h.signIn.service.CompletePasskey(ctx, &signinsvc.PasskeyRequest{
		Entry: *entry, PrincipalID: person, Reason: reason, UserVerified: signed.UserVerified,
		Assertion: session.Assertion{
			AuthTime: at, ProviderID: session.LocalProviderID, PrincipalID: person, Subject: person, UserName: "",
			Email: "", Method: session.MethodPasskey, Names: nil, Groups: nil,
			Factors:       []session.Factor{session.FactorPasskey},
			EmailVerified: false, GroupsPresent: false, AuthTimeKnown: true,
		},
	})

	switch {
	case err == nil:
		length := h.sessionLength(ctx, res.Session)
		if res.BreakGlass {
			// The cookie of a break-glass session ends at its limit too.
			length = min(length, h.signIn.sessions.BreakGlassMaxLength(ctx))
		}

		http.SetCookie(w, sessionCookie(res.Secret.Reveal(), length))
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, signinsvc.ErrReasonRequired):
		// The sign-in service refused the sign-in of the break-glass account
		// and wrote the audit entry. The person knows the passkey, so the
		// answer may name the missing reason.
		h.writeError(ctx, w, errBreakGlassReason)
	case signinsvc.IsRefusal(err):
		h.signIn.passkeyFailures.Fail(ctx)
		h.writeError(ctx, w, errPasskeySignInFailed)
	default:
		h.writeError(ctx, w, err)
	}
}

// passkeySignInError returns the answer of an error of a step of the passkey
// sign-in. A source at its count of open challenges is a 429. A store that
// cannot answer is a 503. Every other error answers the
// text of a failed sign-in: the answer tells nothing about a passkey, a
// person or a ceremony.
func passkeySignInError(err error) error {
	if errors.Is(err, mfa.ErrSourceChallengeLimit) {
		return errPasskeySignInSourceLimit
	}

	if errors.Is(err, mfa.ErrUnavailable) {
		return errPasskeySignInUnavailable
	}

	return errPasskeySignInFailed
}
