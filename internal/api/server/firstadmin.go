package server

// The routes of the first administrator. They make the first person who holds
// the site-administrator role on a new installation.
//
//   - GET /api/server/first-administrator reads the marker "first administrator
//     made" and the open offer, never the code (server:read-first-administrator).
//   - POST /api/server/first-administrator asks for the one-time code of one
//     person (server:offer-first-administrator). Only the static bootstrap
//     administrator may ask. The answer holds the code once.
//   - POST /api/server/first-administrator/redeem sends the code in the console
//     session of the named person (caller:redeem-first-administrator). The
//     session needs its CSRF value and a fresh sign-in.
//
// No route takes an Idempotency-Key: a stored answer of the offer would hold
// the code. The code lives in the body of the two POST routes only. Each
// answer has Cache-Control: no-store.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/firstadminsvc"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Paths of the routes of the first administrator.
const (
	firstAdministratorPath       = "/api/server/first-administrator"
	firstAdministratorRedeemPath = firstAdministratorPath + "/redeem"
	tagFirstAdministrator        = "first-administrator"
	// maxFirstAdministratorBody is the most bytes of the body of the offer and
	// of the redeem. The reason holds at most audit.MaxReasonLen characters of
	// at most four bytes each, and the code holds 43 characters.
	maxFirstAdministratorBody = 4 * 1024
	firstAdministratorRoutes  = 3
)

// The rules that a refusal of this adapter writes to its audit entry. Each is
// a fixed text. None holds a code or text from the request.
const (
	faRuleCallerNotBootstrap = "caller-not-bootstrap"
	faRuleBodyInvalid        = "body-invalid"
	faRuleBodyTooLarge       = "body-too-large"
	faRuleQuery              = "query-not-empty"
	faRuleIdempotencyKey     = "idempotency-key"
	faRuleNoSession          = "no-console-session"
	faRuleStaleSignIn        = "stale-sign-in"
)

// FirstAdministratorService is the service of the first administrator.
// *firstadminsvc.Service meets it.
type FirstAdministratorService interface {
	// State returns the marker and the open offer, never a code.
	State(ctx context.Context) (firstadmin.State, error)
	// Offer makes the one-time code of one person.
	Offer(ctx context.Context, in firstadminsvc.OfferInput) (firstadminsvc.OfferResult, error)
	// Redeem spends the code and makes the first administrator.
	Redeem(ctx context.Context, in firstadminsvc.RedeemInput) (firstadmin.Result, error)
}

// offerFirstAdministratorRequest is the body of the offer.
type offerFirstAdministratorRequest struct {
	Principal string `json:"principal"`
	Reason    string `json:"reason"`
}

// offerFirstAdministratorResponse is the answer of the offer. Code is shown
// here only.
//
// Contract: never remove or rename these fields.
type offerFirstAdministratorResponse struct {
	Principal string `json:"principal"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

// redeemFirstAdministratorRequest is the body of the redeem.
type redeemFirstAdministratorRequest struct {
	Code string `json:"code"`
}

// redeemFirstAdministratorResponse is the answer of the redeem.
//
// Contract: never remove or rename these fields.
type redeemFirstAdministratorResponse struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
	Grant     string `json:"grant"`
	MadeAt    string `json:"madeAt"`
}

// firstAdministratorOfferState is the open offer in the answer of the read. It
// holds no code and no digest.
//
// Contract: never remove or rename these fields.
type firstAdministratorOfferState struct {
	Principal string `json:"principal"`
	CreatedBy string `json:"createdBy"`
	ExpiresAt string `json:"expiresAt"`
}

// firstAdministratorStateResponse is the answer of the read. The fields of
// the marker are empty until the first administrator is made.
//
// Contract: never remove or rename these fields.
type firstAdministratorStateResponse struct {
	Offer     *firstAdministratorOfferState `json:"offer"`
	Principal string                        `json:"principal,omitempty"`
	Grant     string                        `json:"grant,omitempty"`
	Session   string                        `json:"session,omitempty"`
	CreatedBy string                        `json:"createdBy,omitempty"`
	MadeAt    string                        `json:"madeAt,omitempty"`
	Made      bool                          `json:"made"`
}

// The coded answers of this adapter.
var (
	errFirstAdminBodyInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestBodyInvalid, args: nil,
		message: "the request body is not one JSON object with the fields of this route",
	}
	errFirstAdminBodyTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.RequestBodyTooLarge,
		args:    []errcode.Value{errcode.Quantity("maxBytes", maxFirstAdministratorBody)},
		message: "the request body is larger than the limit of this route",
	}
	errFirstAdminQuery = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestInvalidQuery, args: nil,
		message: "this route takes no query parameter: send the values in the body",
	}
	errFirstAdminIdempotencyKey = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestIdempotencyKeyUnsupported, args: nil,
		message: "this route does not take the Idempotency-Key header: send the request without it",
	}
	errFirstAdminAlreadyMade = &codedError{
		status: http.StatusConflict, code: errcode.FirstAdministratorAlreadyMade, args: nil,
		message: "the first administrator is made already: the route is closed. " +
			"To get an administrator again, run opendustpan host create-temp-admin on the server host",
	}
	errFirstAdminCodeInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorCodeInvalid, args: nil,
		message: "the code is not valid: ask the operator for a new code with POST /api/server/first-administrator",
	}
	errFirstAdminPrincipalNotPerson = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "the principal is not a person of this installation: use the ID of an enabled person, not a service",
	}
	errFirstAdminPrincipalDisabled = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "the principal is disabled: enable the person, or use the ID of an enabled person",
	}
	errFirstAdminPrincipalBreakGlass = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "the principal is a break-glass person: use the ID of a person who is not a break-glass person",
	}
	errFirstAdminPrincipalTempAdmin = &codedError{
		status:  http.StatusBadRequest,
		code:    errcode.FirstAdministratorPrincipalInvalid,
		args:    nil,
		message: "the principal is a temporary administrator: use the ID of a person who is not a temporary administrator",
	}
	errFirstAdminPrincipalDevSubject = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "the principal has a dev: subject: use the ID of a person who signs in with a real identity provider",
	}
	errFirstAdminPrincipalBootstrapSubject = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "the principal has the subject of the bootstrap administrator: use the ID of a person",
	}
	errFirstAdminPrincipalNotLive = &codedError{
		status:  http.StatusBadRequest,
		code:    errcode.FirstAdministratorPrincipalInvalid,
		args:    nil,
		message: "the principal is not live: use the ID of an enabled person of this installation, and sign in as that person to redeem",
	}
	// errFirstAdminPrincipalInvalid is the answer of a principal error that
	// names no rule.
	errFirstAdminPrincipalInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorPrincipalInvalid, args: nil,
		message: "this principal cannot be the first administrator: use the ID of an enabled person of this " +
			"installation, not a service, a break-glass person, a temporary administrator, or a dev: user",
	}
	errFirstAdminReasonInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.FirstAdministratorReasonInvalid, args: nil,
		message: "the reason is not valid: send 1 to 500 characters of UTF-8 text with no control character",
	}
	errFirstAdminGrantExists = &codedError{
		status: http.StatusConflict, code: errcode.FirstAdministratorGrantExists, args: nil,
		message: "the person holds the site-administrator role already: no code is spent",
	}
	// errFirstAdminFailed is the answer of an error that no row below names,
	// and of a port that failed. It has no code of the catalog and no text from
	// the cause, so the status is 500 and nothing else.
	errFirstAdminFailed = errors.New("the first administrator request failed")
)

// firstAdministratorRoutes returns the rows of the routes of the first
// administrator. A handler with no service mounts none.
func (h *Handler) firstAdministratorRoutes() []Route {
	if h.firstAdmin == nil {
		return nil
	}

	routes := make([]Route, 0, firstAdministratorRoutes)

	return append(routes,
		Route{
			Method: http.MethodGet, Path: firstAdministratorPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerReadFirstAdministrator},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.readFirstAdministrator,
		},
		Route{
			Method: http.MethodPost, Path: firstAdministratorPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerOfferFirstAdministrator},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.offerFirstAdministrator,
		},
		Route{
			Method: http.MethodPost, Path: firstAdministratorRedeemPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerRedeemFirstAdministrator},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.redeemFirstAdministrator,
		},
	)
}

// firstAdminRequest returns the facts of the request that an audit entry of
// the service records.
func firstAdminRequest(r *http.Request) firstadminsvc.Request {
	return firstadminsvc.Request{
		SourceIP:  clientip.String(r),
		UserAgent: audit.TruncateUserAgent(userAgent(r)),
		RequestID: requestid.FromContext(r.Context()),
		Channel:   callerChannel(r),
	}
}

// firstAdminRefusal names the action of a refusal and the fixed text of the
// rule that failed.
type firstAdminRefusal struct {
	action identity.Action
	rule   string
}

// refuseFirstAdmin writes the failure entry of a refusal that this adapter
// makes before the service runs, and answers err. The entry has the same shape
// as the entry of a refusal of the service: the outcome failure and the change
// "rule" with a fixed text. The write is best effort, as the answer is a
// refusal already.
func (h *Handler) refuseFirstAdmin(
	w http.ResponseWriter, r *http.Request, p identity.Principal, at firstAdminRefusal, err error,
) {
	e := callerEntry(r, p, at.action)
	e.At = h.now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeFailure
	e.Object = identity.ServerObject().String()
	e.Changes = append(e.Changes, audit.Change{Key: "rule", Old: nil, New: at.rule, Layer: "", TakesEffect: ""})

	if _, appendErr := h.audit.AppendServer(context.WithoutCancel(r.Context()), e); appendErr != nil {
		h.failAudit(r.Context())
	}

	h.writeError(r.Context(), w, err)
}

// screenFirstAdminRequest refuses a request that carries a value outside the
// body. The two POST routes take no query parameter and no Idempotency-Key.
// It returns the failed rule and the error, or no rule and no error.
//
// Defense in depth: the decode of the body refuses unknown fields, and no
// handler reads a code from a query or from a header: apidesc allows only the
// declared names. This check stays so that a code that a client puts in a URL,
// which proxies and access logs keep, gets a refusal instead of a quiet answer.
func screenFirstAdminRequest(r *http.Request) (string, error) {
	q := queryOf(r)
	if q.Err() != nil || len(q.Undeclared()) > 0 {
		return faRuleQuery, errFirstAdminQuery
	}

	if len(apidesc.HeaderValues(r, "Idempotency-Key")) > 0 {
		return faRuleIdempotencyKey, errFirstAdminIdempotencyKey
	}

	return "", nil
}

// decodeFirstAdminBody reads the body of r into v with the size limit and with
// no unknown field. It returns the failed rule and the error.
func decodeFirstAdminBody(w http.ResponseWriter, r *http.Request, v any) (string, error) {
	if err := textbody.Decode(w, r, maxFirstAdministratorBody, v); err != nil {
		if errors.Is(err, textbody.ErrTooLarge) {
			return faRuleBodyTooLarge, errFirstAdminBodyTooLarge
		}

		return faRuleBodyInvalid, errFirstAdminBodyInvalid
	}

	return "", nil
}

// readFirstAdministrator answers the marker and the open offer. It never
// answers a code or a digest.
func (h *Handler) readFirstAdministrator(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	noStore(w)

	state, err := h.firstAdmin.State(ctx)
	if err != nil {
		h.writeError(ctx, w, firstAdminError(err))

		return
	}

	body := firstAdministratorStateResponse{
		Made: state.Marker != nil, Offer: nil, Principal: "", Grant: "", Session: "", CreatedBy: "", MadeAt: "",
	}

	if m := state.Marker; m != nil {
		body.Principal = m.PrincipalID
		body.Grant = m.GrantID
		body.Session = m.SessionID
		body.CreatedBy = m.CreatedBy
		body.MadeAt = m.MadeAt.UTC().Format(time.RFC3339)
	}

	if o := state.Offer; o != nil {
		body.Offer = &firstAdministratorOfferState{
			Principal: o.PrincipalID, CreatedBy: o.CreatedBy, ExpiresAt: o.ExpiresAt.UTC().Format(time.RFC3339),
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, body)
}

// offerFirstAdministrator makes the one-time code of one person and answers
// 201 with the code. Only the static bootstrap administrator may ask.
func (h *Handler) offerFirstAdministrator(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	action := identity.ActionServerOfferFirstAdministrator

	noStore(w)

	// Defense in depth: the route authorizes the action, which only a
	// principal with Admin passes, and the service checks the caller again.
	// This check stays so that a personal token with Admin, a temporary
	// administrator and a console session never reach the service, whatever
	// the authorizer of an embedder allows.
	if !firstadminsvc.IsBootstrap(p) {
		h.refuseFirstAdmin(
			w, r, p, firstAdminRefusal{action: action, rule: faRuleCallerNotBootstrap},
			firstAdminError(firstadminsvc.ErrNotBootstrap),
		)

		return
	}

	if rule, err := screenFirstAdminRequest(r); err != nil {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: rule}, err)

		return
	}

	var req offerFirstAdministratorRequest
	if rule, err := decodeFirstAdminBody(w, r, &req); err != nil {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: rule}, err)

		return
	}

	res, err := h.firstAdmin.Offer(ctx, firstadminsvc.OfferInput{
		Caller: p, PrincipalID: req.Principal, Reason: req.Reason, Request: firstAdminRequest(r),
	})
	if err != nil {
		h.writeError(ctx, w, firstAdminError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, offerFirstAdministratorResponse{
		Principal: res.PrincipalID, Code: string(res.Code), ExpiresAt: res.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// redeemFirstAdministrator spends the code and makes the first administrator
// of the console session. It needs the console session of the person, its CSRF
// value (the session checker refuses a request without it) and a fresh
// sign-in. A wrong code spends one unit of the sign-in failure budget.
func (h *Handler) redeemFirstAdministrator(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	action := identity.ActionCallerRedeemFirstAdministrator

	noStore(w)

	// An access token never gives a fresh sign-in, and a person with no
	// session has no sign-in time. Defense in depth: the service and the store
	// check the sign-in time again from the values of the session.
	caller, ok := sessionFromRequest(r)
	if !ok || h.signIn == nil || caller.Match.Owner.Kind != principal.KindPerson || caller.Match.Owner.ID == "" {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: faRuleNoSession}, errFreshStepRequired)

		return
	}

	// The fresh check of this route is its own: it reads the window of the
	// session policy and asks no setting that turns the check off.
	if err := h.checkRedeemFreshness(r, &caller); err != nil {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: faRuleStaleSignIn}, err)

		return
	}

	if rule, err := screenFirstAdminRequest(r); err != nil {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: rule}, err)

		return
	}

	if err := h.admitSignIn(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req redeemFirstAdministratorRequest
	if rule, err := decodeFirstAdminBody(w, r, &req); err != nil {
		h.refuseFirstAdmin(w, r, p, firstAdminRefusal{action: action, rule: rule}, err)

		return
	}

	res, err := h.firstAdmin.Redeem(ctx, firstadminsvc.RedeemInput{
		AuthTime: caller.Match.Session.AuthTime, Caller: p, PrincipalID: caller.Match.Owner.ID,
		SessionID: caller.Match.Session.ID, Code: firstadmin.Code(req.Code), Request: firstAdminRequest(r),
	})
	if err != nil {
		if errors.Is(err, firstadmin.ErrCodeInvalid) {
			h.failSignIn(ctx)
		}

		h.writeError(ctx, w, firstAdminError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, redeemFirstAdministratorResponse{
		Principal: res.PrincipalID, Role: firstadmin.Role, Grant: res.GrantID,
		MadeAt: res.MadeAt.UTC().Format(time.RFC3339),
	})
}

// checkRedeemFreshness refuses a redeem from a console session whose sign-in
// is older than the freshness window of the session policy. A window of 0
// asks at each step, so no sign-in passes, and a sign-in after now is too old.
// It never asks a setting: the redeem needs a fresh sign-in whatever the
// installation settings say.
func (h *Handler) checkRedeemFreshness(r *http.Request, caller *sessionCaller) error {
	window := h.freshnessWindow(r.Context())
	refusal := reauthenticateError{
		url:    reauthenticateURL(caller.Match.Session.Provider),
		window: window.duration(),
	}

	// Defense in depth: sessionauth.Fresh refuses a window of 0 at the
	// comparison below too. This check stays so that this route cannot pass a
	// sign-in if a new version of Fresh accepts a window of 0, and it does not
	// rely on the service or the store.
	if window.asksEachTime() {
		return refusal
	}

	if sessionauth.Fresh(h.signIn.sessions.Now(), caller.Match.Session.AuthTime, window.duration()) {
		return nil
	}

	return refusal
}

// firstAdminError maps an error of the service or the store to the answer. A
// known sentinel gets its coded answer. An error that no row names, and a port
// that failed, get the plain 500 with no text of the cause. The mapping fails
// closed: no unknown error gives a success or a detail.
func firstAdminError(err error) error {
	switch {
	case errors.Is(err, firstadminsvc.ErrNotBootstrap):
		return fmt.Errorf("%w: the offer needs the bootstrap administrator", identity.ErrDenied)
	case errors.Is(err, firstadmin.ErrAlreadyMade):
		return errFirstAdminAlreadyMade
	case errors.Is(err, firstadmin.ErrCodeInvalid):
		return errFirstAdminCodeInvalid
	case errors.Is(err, firstadmin.ErrPrincipalInvalid):
		return firstAdminPrincipalError(err)
	case errors.Is(err, firstadmin.ErrReasonInvalid):
		return errFirstAdminReasonInvalid
	case errors.Is(err, firstadmin.ErrGrantExists):
		return errFirstAdminGrantExists
	case errors.Is(err, firstadmin.ErrSessionInvalid):
		// The stored session is gone, ended, idle, expired or of another person.
		// The caller needs a new sign-in, as for a stale sign-in.
		return errFreshStepRequired
	case errors.Is(err, firstadmin.ErrStaleSignIn):
		// The store refused the sign-in time that the handler accepted. The
		// answer is the one of a stale sign-in, with no path of a new sign-in:
		// the handler gives that path.
		return errFreshStepRequired
	default:
		return errFirstAdminFailed
	}
}

// firstAdminPrincipalError maps an error of ErrPrincipalInvalid to the answer
// of the rule that failed. An error that names no rule gets the answer that
// lists all rules.
func firstAdminPrincipalError(err error) error {
	rules := []struct {
		rule   error
		answer error
	}{
		{firstadmin.ErrPrincipalNotPerson, errFirstAdminPrincipalNotPerson},
		{firstadmin.ErrPrincipalDisabled, errFirstAdminPrincipalDisabled},
		{firstadmin.ErrPrincipalBreakGlass, errFirstAdminPrincipalBreakGlass},
		{firstadmin.ErrPrincipalTempAdmin, errFirstAdminPrincipalTempAdmin},
		{firstadmin.ErrPrincipalDevSubject, errFirstAdminPrincipalDevSubject},
		{firstadmin.ErrPrincipalBootstrapSubject, errFirstAdminPrincipalBootstrapSubject},
		{firstadmin.ErrPrincipalNotLive, errFirstAdminPrincipalNotLive},
	}

	for _, r := range rules {
		if errors.Is(err, r.rule) {
			return r.answer
		}
	}

	return errFirstAdminPrincipalInvalid
}
