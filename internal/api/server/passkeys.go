package server

// The routes of the passkeys of the signed-in person. A passkey is a second
// factor and, later, a sign-in of its own. These routes add, list and remove
// the passkeys of the caller. They sign no one in.
//
//   - POST /api/dustpan/v1/me/passkeys/register/begin starts a register
//     ceremony and answers the creation options (caller:manage-sign-in, a
//     fresh authentication).
//   - POST /api/dustpan/v1/me/passkeys/register/finish ends the ceremony with
//     the response of the authenticator and a name, and stores the passkey
//     (caller:manage-sign-in, a fresh authentication).
//   - GET /api/dustpan/v1/me/passkeys lists the passkeys of the caller
//     (caller:read).
//   - DELETE /api/dustpan/v1/me/passkeys/{id} removes one passkey of the
//     caller (caller:manage-sign-in, a fresh authentication).
//
// The service has no rename, so these routes have none.
//
// Every route needs the console session of a person. An access token never
// adds, lists or removes a passkey. Every refusal writes an audit entry: the
// service writes the entry of a refusal that it makes, and this adapter writes
// the entry of a refusal that it makes before the service runs.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Paths of the passkey routes.
const (
	ownKeysPath         = "/api/dustpan/v1/me/passkeys"
	passkeyBeginPath    = ownKeysPath + "/register/begin"
	passkeyFinishPath   = ownKeysPath + "/register/finish"
	passkeyPath         = ownKeysPath + "/{id}"
	tagPasskeys         = "passkeys"
	passkeyRouteCount   = 4
	passkeyEnvelopeSize = 4 * 1024
	// maxPasskeyBody is the most bytes of a request body of the passkey
	// routes: the response of an authenticator at its limit, and the name and
	// the JSON around it.
	maxPasskeyBody = mfa.MaxResponseBytes + passkeyEnvelopeSize
)

// Limits of the begin of a register ceremony. Each begin writes one challenge
// row, so a person has a budget of begins in a window. The count is of one
// server process: the instances of one database each keep their own count.
const (
	passkeyBeginLimit  = 10
	passkeyBeginWindow = 5 * time.Minute
	// passkeyBeginPeople is the most people that the limiter tracks at one
	// time. With the table full of live windows, the limiter refuses, which
	// fails closed.
	passkeyBeginPeople = 10_000
)

// PasskeyService is the service of the passkeys. *mfasvc.Service meets it.
type PasskeyService interface {
	// BeginRegistration starts a register ceremony and returns the JSON of the
	// creation options.
	BeginRegistration(ctx context.Context, a mfasvc.Actor, p mfa.Person) ([]byte, error)
	// FinishRegistration ends the register ceremony of the response and stores
	// the passkey under the name.
	FinishRegistration(
		ctx context.Context, a mfasvc.Actor, p mfa.Person, name string, response []byte,
	) (mfa.Passkey, error)
	// Passkeys returns the passkeys of the caller.
	Passkeys(ctx context.Context, a mfasvc.Actor) ([]mfa.Passkey, error)
	// RemovePasskey removes the passkey id of the caller.
	RemovePasskey(ctx context.Context, a mfasvc.Actor, id string) error
}

// finishPasskeyRequest is the body of the end of a register ceremony.
type finishPasskeyRequest struct {
	Name     string      `json:"name"`
	Response passkeyJSON `json:"response"`
}

// passkeyCreationOptions is the answer of the begin of a register ceremony:
// the options that a browser gives to navigator.credentials.create. The
// handler writes the bytes of the relying party, and this type only describes
// them.
type passkeyCreationOptions struct {
	PublicKey passkeyJSON `json:"publicKey"`
}

// passkeyJSON is a JSON object of the WebAuthn protocol, for example the
// credential that a browser makes from the creation options. The service
// parses it, so the route keeps its bytes.
type passkeyJSON []byte

// errPasskeyJSON reports a value that is not one JSON object.
var errPasskeyJSON = errors.New("the value is not one JSON object")

// UnmarshalJSON keeps the bytes of the JSON value. It refuses a value that is
// not an object, so the service never parses a string, a number or null.
func (c *passkeyJSON) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] != '{' {
		return errPasskeyJSON
	}

	*c = append((*c)[:0], b...)

	return nil
}

// MarshalJSON returns the bytes of the JSON value, or null for none. It
// refuses bytes that are not valid JSON.
func (c passkeyJSON) MarshalJSON() ([]byte, error) {
	if len(c) == 0 {
		return []byte("null"), nil
	}

	if !json.Valid(c) {
		return nil, errPasskeyJSON
	}

	return c, nil
}

// passkeyBody is one passkey of the caller. It holds no credential ID and no
// public key. lastUsedAt and suspendedAt are empty when the passkey has no
// such time.
//
// Contract: never remove or rename these fields.
type passkeyBody struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Transports     []string `json:"transports"`
	BackupEligible bool     `json:"backupEligible"`
	CreatedAt      string   `json:"createdAt"`
	LastUsedAt     string   `json:"lastUsedAt"`
	SuspendedAt    string   `json:"suspendedAt"`
}

// passkeysResponse is the passkeys of the caller, in the order of the add.
//
// Contract: never remove or rename these fields.
type passkeysResponse struct {
	Passkeys []passkeyBody `json:"passkeys"`
}

// passkeyBodyOf returns the body of the passkey k.
func passkeyBodyOf(k mfa.Passkey) passkeyBody {
	b := passkeyBody{
		ID: k.ID, Name: k.Name, Transports: k.Transports, BackupEligible: k.BackupEligible,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339), LastUsedAt: "", SuspendedAt: "",
	}

	if b.Transports == nil {
		b.Transports = []string{}
	}

	if !k.LastUsedAt.IsZero() {
		b.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
	}

	if !k.SuspendedAt.IsZero() {
		b.SuspendedAt = k.SuspendedAt.UTC().Format(time.RFC3339)
	}

	return b
}

// passkeyRoutes returns the rows of the passkey routes. A handler with no
// passkey service, or with no console sign-in, mounts none: a passkey needs
// the session of a person.
func (h *Handler) passkeyRoutes() []Route {
	if h.passkeys == nil || h.signIn == nil {
		return nil
	}

	manage := []identity.Action{identity.ActionCallerManageSignIn}
	read := []identity.Action{identity.ActionCallerRead}

	routes := make([]Route, 0, passkeyRouteCount)

	return append(routes,
		Route{
			Method: http.MethodPost, Path: passkeyBeginPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(identity.ActionCallerManageSignIn, h.beginPasskey),
		},
		Route{
			Method: http.MethodPost, Path: passkeyFinishPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(identity.ActionCallerManageSignIn, h.finishPasskey),
		},
		Route{
			Method: http.MethodGet, Path: ownKeysPath, Scope: ScopeAuthorized, Actions: read,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false, Serve: h.listPasskeys,
		},
		Route{
			Method: http.MethodDelete, Path: passkeyPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(identity.ActionCallerManageSignIn, h.removePasskey),
		},
	)
}

// passkeyRefusal names the action and the object of the audit entry of a
// refusal that the adapter makes.
type passkeyRefusal struct {
	action string
	object string
}

// unknownPasskey returns the refusal of the action, for a step that names no
// person and no passkey yet.
func unknownPasskey(action string) passkeyRefusal {
	return passkeyRefusal{action: action, object: mfasvc.Object("unknown")}
}

// passkeyCaller returns the console session of the request and the person of
// its owner. A request with no session, or with the session of a principal
// that is not a person, answers mfa.session-required and returns false. The
// refusal has the audit entry at.
//
// Defense in depth: the route check authorizes the action and fresh checks
// the session for a write. This check stays so that a route that skips either
// layer never acts for an access token, and never for a service principal.
func (h *Handler) passkeyCaller(
	w http.ResponseWriter, r *http.Request, p identity.Principal, at passkeyRefusal,
) (sessionCaller, mfa.Person, bool) {
	caller, ok := sessionFromRequest(r)
	if !ok || caller.Match.Owner.Kind != principal.KindPerson || caller.Match.Owner.ID == "" {
		h.refusePasskey(w, r, p, at, errPasskeySessionRequired)

		var nobody mfa.Person

		return caller, nobody, false
	}

	owner := caller.Match.Owner
	person := mfa.Person{ID: owner.ID, Name: owner.Subject, DisplayName: cmp.Or(owner.DisplayName, owner.Subject)}

	return caller, person, true
}

// passkeyActor returns the actor of a step of the caller: the audit entry of
// the request, and the principal and the session of the console session.
func passkeyActor(r *http.Request, p identity.Principal, caller sessionCaller, action identity.Action) mfasvc.Actor {
	return mfasvc.Actor{
		Entry: callerEntry(r, p, action), PrincipalID: caller.Match.Owner.ID, SessionID: caller.Match.Session.ID,
	}
}

// refusePasskey appends the audit entry at of a refusal that the adapter makes
// before the service runs, and answers err.
func (h *Handler) refusePasskey(
	w http.ResponseWriter, r *http.Request, p identity.Principal, at passkeyRefusal, err error,
) {
	h.appendServerChange(r, p, serverChange{
		at: h.now(), action: identity.Action(at.action), object: at.object, outcome: audit.OutcomeDenied,
	})
	h.writeError(r.Context(), w, err)
}

// beginPasskey starts a register ceremony and answers 200 with the creation
// options. Each begin writes a challenge row, so the person has a budget of
// begins.
func (h *Handler) beginPasskey(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	caller, person, ok := h.passkeyCaller(w, r, p, unknownPasskey(mfasvc.ActionAddRefused))
	if !ok {
		return
	}

	if wait, allowed := h.passkeyBegins.allow(person.ID, h.now()); !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(wait/time.Second)+1, 10))

		// One audit entry for each person and window: a person at the limit
		// cannot fill the audit log with refusals.
		if h.passkeyBegins.firstRefusal(person.ID) {
			h.refusePasskey(w, r, p, passkeyRefusal{action: mfasvc.ActionAddRefused, object: principal.Ref(person.ID)},
				errPasskeyRateLimited)
		} else {
			h.writeError(ctx, w, errPasskeyRateLimited)
		}

		return
	}

	actor := passkeyActor(r, p, caller, identity.ActionCallerManageSignIn)

	options, err := h.passkeys.BeginRegistration(ctx, actor, person)
	if err != nil {
		h.writeError(ctx, w, passkeyError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, json.RawMessage(options))
}

// finishPasskey ends a register ceremony and answers 201 with the passkey.
func (h *Handler) finishPasskey(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	caller, person, ok := h.passkeyCaller(w, r, p, unknownPasskey(mfasvc.ActionAddRefused))
	if !ok {
		return
	}

	refused := passkeyRefusal{action: mfasvc.ActionAddRefused, object: principal.Ref(person.ID)}

	var req finishPasskeyRequest
	if err := textbody.Decode(w, r, maxPasskeyBody, &req); err != nil {
		h.refusePasskey(w, r, p, refused, passkeyBodyError(err))

		return
	}

	// Defense in depth: the service checks the name again before it stores
	// the passkey. This check stays so that a name with a hidden character
	// never reaches the service, the ceremony or the audit entry.
	if err := mfa.ValidatePasskeyName(req.Name); err != nil {
		h.refusePasskey(w, r, p, refused, ceremonyError(err))

		return
	}

	actor := passkeyActor(r, p, caller, identity.ActionCallerManageSignIn)

	key, err := h.passkeys.FinishRegistration(ctx, actor, person, req.Name, req.Response)
	if err != nil {
		h.writeError(ctx, w, ceremonyError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, passkeyBodyOf(key))
}

// listPasskeys answers the passkeys of the caller.
func (h *Handler) listPasskeys(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	caller, _, ok := h.passkeyCaller(w, r, p, unknownPasskey(mfasvc.ActionListRefused))
	if !ok {
		return
	}

	keys, err := h.passkeys.Passkeys(ctx, passkeyActor(r, p, caller, identity.ActionCallerRead))
	if err != nil {
		h.writeError(ctx, w, passkeyError(err))

		return
	}

	out := passkeysResponse{Passkeys: make([]passkeyBody, 0, len(keys))}
	for i := range keys {
		out.Passkeys = append(out.Passkeys, passkeyBodyOf(keys[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// removePasskey removes one passkey of the caller and answers 204. A passkey
// of another person answers like a passkey that does not exist.
func (h *Handler) removePasskey(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	refused := unknownPasskey(mfasvc.ActionRemoveRefused)

	caller, _, ok := h.passkeyCaller(w, r, p, refused)
	if !ok {
		return
	}

	id := r.PathValue("id")

	// Defense in depth: the service checks the ID again. This check stays so
	// that a path with a character that no passkey ID has never reaches the
	// service or the audit object.
	if !ident.Valid(id, mfa.MaxIDLength) {
		h.refusePasskey(w, r, p, refused, passkeyError(mfa.ErrNotFound))

		return
	}

	actor := passkeyActor(r, p, caller, identity.ActionCallerManageSignIn)

	if err := h.passkeys.RemovePasskey(ctx, actor, id); err != nil {
		h.writeError(ctx, w, passkeyError(err))

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// The errors of the passkey routes that no domain sentinel carries.
var (
	errPasskeySessionRequired = &codedError{
		status: http.StatusForbidden, code: errcode.MFASessionRequired, args: nil,
		message: "passkeys belong to a person: sign in to the console as a person to use them",
	}
	errPasskeyRateLimited = &codedError{
		status: http.StatusTooManyRequests, code: errcode.MFARateLimited, args: nil,
		message: "too many passkey ceremonies started: try again in a few minutes",
	}
	errPasskeyBodyInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestBodyInvalid, args: nil,
		message: "the request body is not a valid JSON object of this route",
	}
	errPasskeyBodyTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.RequestBodyTooLarge,
		args:    []errcode.Value{errcode.Quantity("maxBytes", maxPasskeyBody)},
		message: "the request body is larger than the limit of the passkey routes",
	}
)

// passkeyBodyError returns the answer for a body that textbody refused. It
// never echoes the body.
func passkeyBodyError(err error) error {
	if errors.Is(err, textbody.ErrTooLarge) {
		return errPasskeyBodyTooLarge
	}

	return errPasskeyBodyInvalid
}

// passkeyMapping is one row of the answers of the passkey errors.
type passkeyMapping struct {
	err    error
	answer *codedError
}

// passkeyErrors returns the answers of the errors of the passkey service, in
// the order of the check. The texts name the rule and never a challenge, a key
// or a response. ErrNotFound is not in the table: its answer depends on the
// route.
func passkeyErrors() []passkeyMapping {
	return []passkeyMapping{
		{mfa.ErrWrongPerson, &codedError{
			status: http.StatusForbidden, code: errcode.MFACeremonyWrongPerson, args: nil,
			message: "the passkey ceremony belongs to another person or session: start it again",
		}},
		{mfa.ErrVerification, &codedError{
			status: http.StatusBadRequest, code: errcode.MFACeremonyFailed, args: nil,
			message: "the response of the authenticator failed a check: start the ceremony again",
		}},
		{mfa.ErrLimit, &codedError{
			status: http.StatusConflict, code: errcode.MFAKeyLimit,
			args:    []errcode.Value{errcode.Quantity("limit", mfa.MaxPasskeys)},
			message: "the person holds the most passkeys: remove one before you add another",
		}},
		{mfa.ErrAlreadyExists, &codedError{
			status: http.StatusConflict, code: errcode.MFAKeyExists, args: nil,
			message: "a person already holds the passkey of this authenticator",
		}},
		{mfa.ErrSuspended, &codedError{
			status: http.StatusConflict, code: errcode.MFAKeySuspended, args: nil,
			message: "the passkey is suspended because its signature counter went back",
		}},
		{mfa.ErrLastCredential, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserLastCredential, args: nil,
			message: "the change removes the last sign-in credential of the person",
		}},
		{localuser.ErrDemoFixed, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserDemoFixed, args: nil,
			message: "the credentials of the demo user cannot change",
		}},
		{mfa.ErrUnavailable, &codedError{
			status: http.StatusServiceUnavailable, code: errcode.MFAUnavailable, args: nil,
			message: "the passkeys cannot answer: try again later",
		}},
		{mfa.ErrInvalid, &codedError{
			status: http.StatusBadRequest,
			code:   errcode.MFAKeyInvalid,
			args:   nil,
			message: "the passkey name or the response is not valid: a name has 1 to 100 ASCII letters, digits, '.', '_' " +
				"and '-', and does not start with '-' or '.'; a response has at most 65,536 bytes",
		}},
	}
}

// passkeyError returns the coded answer of an error of a passkey step that
// names a passkey: a missing record is a passkey that the person does not
// have, and answers 404. An error that the table does not name passes as it
// is, and the writer answers it as an internal error with no detail.
func passkeyError(err error) error {
	for _, m := range passkeyErrors() {
		if errors.Is(err, m.err) {
			return m.answer
		}
	}

	if errors.Is(err, mfa.ErrNotFound) {
		return &codedError{
			status: http.StatusNotFound, code: errcode.MFAKeyNotFound, args: nil,
			message: "the person has no passkey with this ID",
		}
	}

	return err
}

// ceremonyError returns the coded answer of an error of the end of a register
// ceremony: a missing record is a ceremony with no open challenge, and answers
// 400. It answers every other error as passkeyError does.
func ceremonyError(err error) error {
	for _, m := range passkeyErrors() {
		if errors.Is(err, m.err) {
			return m.answer
		}
	}

	if errors.Is(err, mfa.ErrNotFound) {
		return &codedError{
			status: http.StatusBadRequest, code: errcode.MFACeremonyNotOpen, args: nil,
			message: "no open passkey ceremony has this challenge: start the ceremony again",
		}
	}

	return err
}

// beginLimiter limits the begins of the register ceremony of each person in
// a fixed window. It keeps its counts in memory.
type beginLimiter struct {
	mu      sync.Mutex
	windows map[string]beginWindow
}

// beginWindow is the count of begins of one person since start.
type beginWindow struct {
	start time.Time
	count int
	// audited is true after the refusal of this window has an audit entry.
	audited bool
}

// newBeginLimiter returns an empty limiter.
func newBeginLimiter() *beginLimiter {
	return &beginLimiter{mu: sync.Mutex{}, windows: map[string]beginWindow{}}
}

// allow counts one begin of the person at now. It returns false and the wait
// until the window ends when the person used the budget of the window. With
// passkeyBeginPeople live windows and a new person, it returns false: it fails
// closed.
func (l *beginLimiter) allow(person string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, found := l.windows[person]

	if found && now.Sub(w.start) < passkeyBeginWindow {
		if w.count >= passkeyBeginLimit {
			return passkeyBeginWindow - now.Sub(w.start), false
		}

		w.count++
		l.windows[person] = w

		return 0, true
	}

	if !found && len(l.windows) >= passkeyBeginPeople {
		l.prune(now)

		if len(l.windows) >= passkeyBeginPeople {
			return passkeyBeginWindow, false
		}
	}

	l.windows[person] = beginWindow{start: now, count: 1, audited: false}

	return 0, true
}

// firstRefusal reports whether the refusal of the person is the first one in
// the window, and marks the window. A person with no window, which is the
// refusal of a full table, always counts as the first.
func (l *beginLimiter) firstRefusal(person string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, found := l.windows[person]
	if !found {
		return true
	}

	first := !w.audited
	w.audited = true
	l.windows[person] = w

	return first
}

// prune deletes the windows that ended at now.
func (l *beginLimiter) prune(now time.Time) {
	for person, w := range l.windows {
		if now.Sub(w.start) >= passkeyBeginWindow {
			delete(l.windows, person)
		}
	}
}
