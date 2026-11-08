package server

// The routes of the local users and of the password sign-in:
//
//   - POST /api/dustpan/v1/sign-in/password signs a browser in with a user
//     name and a password. It is public, and it spends the sign-in budget of
//     the source address on each failure.
//   - POST /api/dustpan/v1/local-users/redeem sets a password with a set-up
//     code or a reset code. It is public: the code is the credential.
//   - POST /api/dustpan/v1/local-users makes a local user and its set-up
//     code (server:manage-sign-in).
//   - GET /api/dustpan/v1/local-users/policy answers the effective password
//     policy (server:manage-sign-in).
//   - POST /api/dustpan/v1/local-users/reset makes a reset code
//     (server:manage-sign-in, a fresh authentication).
//   - POST /api/dustpan/v1/me/password changes the own password
//     (caller:manage-sign-in).

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// Paths of the local user routes.
const (
	localSignInPath          = "/api/dustpan/v1/sign-in/password"
	localUsersPath           = "/api/dustpan/v1/local-users"
	localUserRedeemPath      = "/api/dustpan/v1/local-users/redeem"
	localUserPolicyPath      = "/api/dustpan/v1/local-users/policy"
	localUserResetPath       = "/api/dustpan/v1/local-users/reset"
	localUserUnlockPath      = "/api/dustpan/v1/local-users/unlock"
	localUserLocksPath       = "/api/dustpan/v1/local-users/locks"
	ownSignInChangePath      = "/api/dustpan/v1/me/password"
	maxLocalUserBody         = 8 * 1024
	tagLocalUsers            = "local-users"
	passwordSignInAuditActor = "sign-in:" + localuser.ProviderID
)

// passwordSignInRequest is the body of a password sign-in.
type passwordSignInRequest struct {
	UserName string `json:"userName"`
	Password string `json:"password"`
}

// redeemRequest is the body of the use of a set-up code or a reset code.
type redeemRequest struct {
	OneTimeCode string `json:"oneTimeCode"`
	Password    string `json:"password"`
}

// createLocalUserRequest is the body of a new local user.
type createLocalUserRequest struct {
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Reason      string `json:"reason"`
}

// createLocalUserResponse is a new local user with its set-up code. The
// server shows the code once.
//
// Contract: never remove or rename these fields.
type createLocalUserResponse struct {
	PrincipalID    string `json:"principal"`
	UserID         string `json:"localUser"`
	UserName       string `json:"userName"`
	Subject        string `json:"subject"`
	SetUpCode      string `json:"setUpCode"`
	SetUpExpiresAt string `json:"setUpExpiresAt"`
}

// resetLocalUserRequest is the body of a reset of a password.
type resetLocalUserRequest struct {
	PrincipalID        string `json:"principal"`
	Reason             string `json:"reason"`
	RevokeAccessTokens bool   `json:"revokeAccessTokens"`
}

// resetLocalUserResponse is a new reset code. The server shows it once.
//
// Contract: never remove or rename these fields.
type resetLocalUserResponse struct {
	ResetCode string `json:"resetCode"`
	ExpiresAt string `json:"expiresAt"`
}

// unlockLocalUserRequest is the body of an unlock of the name of a local
// user.
type unlockLocalUserRequest struct {
	Principal string `json:"principal"`
	Reason    string `json:"reason"`
}

// unlockLocalUserResponse is the count of the counts that an unlock cleared.
//
// Contract: never remove or rename these fields.
type unlockLocalUserResponse struct {
	Cleared int64 `json:"cleared"`
}

// localUserLockBody is the state of one count of the name of a local user.
// lockedUntil is empty when no lock holds.
//
// Contract: never remove or rename these fields.
type localUserLockBody struct {
	Scope       string `json:"scope"`
	Source      string `json:"source"`
	LockedUntil string `json:"lockedUntil"`
	Failures    int    `json:"failures"`
	Level       int    `json:"level"`
}

// localUserLocksResponse is the state of each count of the name of a local
// user.
//
// Contract: never remove or rename these fields.
type localUserLocksResponse struct {
	Locks []localUserLockBody `json:"locks"`
}

// changeOwnPasswordRequest is the body of a change of the own password. An
// empty current password needs a session with a fresh authentication.
type changeOwnPasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// localUserPolicyResponse is the effective password policy.
//
// Contract: never remove or rename these fields.
type localUserPolicyResponse struct {
	Enabled                 bool   `json:"enabled"`
	EnabledSource           string `json:"enabledSource"`
	IdentityProvider        bool   `json:"identityProvider"`
	MinLength               int    `json:"minLength"`
	MaxLength               int    `json:"maxLength"`
	RequireLettersAndDigits bool   `json:"requireLettersAndDigits"`
	ResetLifetimeSeconds    int64  `json:"resetLifetimeSeconds"`
	// LockPrune is the effective schedule of the prune of the counts of the
	// lockout by name.
	LockPrune localUserLockPruneBody `json:"lockPrune"`
	// LockRules is the effective set of rules of the lockout by name.
	LockRules localUserLockRulesBody `json:"lockRules"`
}

// localUserLockRulesBody is the effective set of rules of the lockout by
// name. The password rule counts the failures of one user name from one
// source prefix. The password name rule counts the failures of one user name
// from every source, and its lock holds for every source.
//
// Contract: never remove or rename these fields.
type localUserLockRulesBody struct {
	Password     localUserLockRuleBody     `json:"password"`
	PasswordName localUserLockRuleBody     `json:"passwordName"`
	SourcePrefix localUserSourcePrefixBody `json:"sourcePrefix"`
}

// localUserLockRuleBody is one rule of the lockout by name. The failure that
// takes a count to Limit sets a lock of LockSeconds. Each new lock of the
// same name, less than ResetSeconds after the earlier lock ended, lasts
// twice the lock before, up to MaxLockSeconds. A rule with MaxLockSeconds
// equal to LockSeconds never doubles.
//
// Contract: never remove or rename these fields.
type localUserLockRuleBody struct {
	Limit          int   `json:"limit"`
	LockSeconds    int64 `json:"lockSeconds"`
	MaxLockSeconds int64 `json:"maxLockSeconds"`
	ResetSeconds   int64 `json:"resetSeconds"`
}

// localUserSourcePrefixBody is the length of the source prefix of the
// password rule, in bits: the whole IPv4 address, and the first bits of an
// IPv6 address.
//
// Contract: never remove or rename these fields.
type localUserSourcePrefixBody struct {
	IPv4Bits int `json:"ipv4Bits"`
	IPv6Bits int `json:"ipv6Bits"`
}

// lockRuleBody returns the body of the rule r.
func lockRuleBody(r authgate.LockRule) localUserLockRuleBody {
	return localUserLockRuleBody{
		Limit: r.Limit, LockSeconds: int64(r.LockFor / time.Second), MaxLockSeconds: int64(r.MaxLockFor / time.Second),
		ResetSeconds: int64(r.ResetAfter / time.Second),
	}
}

// localUserLockPruneBody is the effective schedule of the prune of the
// counts of the lockout by name: the time between two runs, the age past the
// last failure of a count that a run can delete, and the most counts that
// one run deletes.
//
// Contract: never remove or rename these fields.
type localUserLockPruneBody struct {
	IntervalSeconds int64 `json:"intervalSeconds"`
	AgeSeconds      int64 `json:"ageSeconds"`
	MaxRows         int   `json:"maxRows"`
	MaxEndedLocks   int   `json:"maxEndedLocks"`
}

// errLocalUsersUnavailable is the answer of a local user route on a server
// that keeps no local users.
var errLocalUsersUnavailable = &codedError{
	status: http.StatusNotImplemented, code: errcode.LocalUserUnavailable, args: nil,
	message: "this server keeps no local users",
}

// errLocalUserBody is the answer of a body that is not the JSON object of the
// route.
var errLocalUserBody = &codedError{
	status: http.StatusBadRequest, code: errcode.RequestBodyInvalid, args: nil,
	message: "the request body is not a valid JSON object of this route",
}

// localUserRoutes returns the rows of the local user routes. A handler with no
// local user service mounts none. The routes that check a password or a
// one-time code need the console sign-in, for its session, and its password
// budget: with either one missing, they are not mounted.
func (h *Handler) localUserRoutes() []Route {
	if h.localUsers == nil {
		return nil
	}

	manage := []identity.Action{identity.ActionServerManageSignIn}
	own := []identity.Action{identity.ActionCallerManageSignIn}

	routes := make([]Route, 0, localUserRouteCount)
	routes = append(routes, []Route{
		{
			Method: http.MethodPost, Path: localUsersPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, Serve: h.createLocalUser,
		},
		{
			Method: http.MethodGet, Path: localUserPolicyPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, Serve: h.getLocalUserPolicy,
		},
		{
			Method: http.MethodPost, Path: localUserResetPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil,
			Serve: h.fresh(identity.ActionServerManageSignIn, h.resetLocalUser),
		},
		{
			Method: http.MethodPost, Path: localUserUnlockPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, Serve: h.unlockLocalUser,
		},
		{
			Method: http.MethodGet, Path: localUserLocksPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, Serve: h.getLocalUserLocks,
		},
	}...)

	if h.signIn == nil || h.signIn.passwords == nil {
		return routes
	}

	return append(routes,
		Route{
			Method: http.MethodPost, Path: localUserRedeemPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, Serve: h.redeemLocalUserCode,
		},
		Route{
			Method: http.MethodPost, Path: ownSignInChangePath, Scope: ScopeAuthorized, Actions: own,
			Resolve: resolveCaller, ActionsFor: nil, Serve: h.changeOwnPassword,
		},
		Route{
			Method: http.MethodPost, Path: localSignInPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, Serve: h.passwordSignIn,
		},
	)
}

// localUserRouteCount is the count of the local user routes: three to
// manage the local users and three that check a password or a code.
const localUserRouteCount = 6

// admitPassword waits or refuses as the password budget of the address of
// the request says. A handler with no password budget refuses: it fails
// closed.
//
// Defense in depth: localUserRoutes mounts no password route without the
// budget. This check stays so that a change to the route table never serves
// a password check with no limit.
func (h *Handler) admitPassword(ctx context.Context) error {
	if h.signIn == nil || h.signIn.passwords == nil {
		return errLocalUsersUnavailable
	}

	return h.signIn.passwords.Admit(ctx)
}

// failPassword spends one unit of the password budget of the address of the
// request.
func (h *Handler) failPassword(ctx context.Context) {
	if h.signIn != nil && h.signIn.passwords != nil {
		h.signIn.passwords.Fail(ctx)
	}
}

// noReferrer keeps the browser from sending the URL of a page with a code to
// another site, and keeps a proxy from storing the answer.
func noReferrer(w http.ResponseWriter) {
	noStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// decodeLocalUserBody decodes the strict JSON body of r into v. A body that
// is not one JSON object with the fields of v answers errLocalUserBody, and
// never echoes the body: it may hold a password or a code.
func decodeLocalUserBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := textbody.Decode(w, r, maxLocalUserBody, v); err != nil {
		return errLocalUserBody
	}

	return nil
}

// passwordSignIn signs a browser in with a user name and a password. It
// answers 204 with the session cookie. Each failure spends one unit of the
// sign-in budget of the source address and answers 401 with one text.
func (h *Handler) passwordSignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noReferrer(w)

	// Defense in depth: localUserRoutes mounts this route only with a console
	// sign-in and a local user service. This check stays so that a change to
	// the route table never serves a sign-in with no session service.
	if h.signIn == nil || h.localUsers == nil {
		h.writeError(ctx, w, errLocalUsersUnavailable)

		return
	}

	// Login CSRF: refuse a request of another site before the budget and
	// before any store call. It spends nothing.
	if !h.signIn.checker.SignInFromBody(r) {
		h.recordOtherSiteSignIn(ctx, r, passwordSignInAuditActor)
		h.writeError(ctx, w, localUserError(localuser.ErrSignInFailed))

		return
	}

	if err := h.admitPassword(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req passwordSignInRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.failPassword(ctx)
		h.writeError(ctx, w, err)

		return
	}

	base := signInEntry(r, passwordSignInAuditActor, session.ActionSignIn)

	in, err := h.localUsers.SignIn(ctx, base, req.UserName, req.Password)
	if err != nil {
		if errors.Is(err, localuser.ErrSignInFailed) {
			h.failPassword(ctx)
		}

		h.writeError(ctx, w, localUserError(err))

		return
	}

	res, ok := h.completeLocal(w, r, in)
	if !ok {
		return
	}

	h.clearNameCounts(ctx, in)

	http.SetCookie(w, sessionCookie(res.Secret.Reveal(), h.sessionLength(ctx, res.Session)))
	w.WriteHeader(http.StatusNoContent)
}

// completeLocal gives the password sign-in in to the sign-in service. It
// returns false after it answered a refusal or an error. A refusal has its
// audit entry already. A failed Assertion, a reset, a password change or a
// disable after the check of the password fails closed with the answer of a
// wrong password.
func (h *Handler) completeLocal(
	w http.ResponseWriter, r *http.Request, in localusersvc.SignedIn,
) (signinsvc.Result, bool) {
	ctx := r.Context()

	res, err := h.signIn.service.CompleteLocal(ctx, &signinsvc.LocalRequest{
		Entry: signInEntry(r, "", session.ActionSignIn), PrincipalID: in.PrincipalID, UserID: in.UserID,
		Assertion: in.Assertion, Generation: in.SessionGeneration, ChangeRequired: in.ChangeRequired,
	})
	if err == nil {
		return res, true
	}

	if signinsvc.IsRefusal(err) {
		h.failPassword(ctx)
		h.writeError(ctx, w, localUserError(localuser.ErrSignInFailed))

		return res, false
	}

	h.writeError(ctx, w, err)

	return res, false
}

// clearNameCounts clears the counts of the name of the password sign-in in.
// Only the end of a full sign-in clears them, after the commit of the
// session. A failed clear leaves the counts, which is the safe direction, so
// the sign-in still succeeds.
func (h *Handler) clearNameCounts(ctx context.Context, in localusersvc.SignedIn) {
	if clearErr := h.localUsers.ClearLocks(ctx, in); clearErr != nil {
		h.logger.WarnContext(ctx, "the counts of a user name were not cleared after a sign-in",
			slog.String("cause", causeType(clearErr)))
	}
}

// redeemLocalUserCode sets a password with a set-up code or a reset code. It
// answers 204. A code that is not valid spends one unit of the sign-in budget
// of the source address.
func (h *Handler) redeemLocalUserCode(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noReferrer(w)

	if err := h.admitPassword(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req redeemRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	base := signInEntry(r, passwordSignInAuditActor, session.ActionSignIn)

	if err := h.localUsers.Redeem(ctx, base, req.OneTimeCode, req.Password); err != nil {
		if errors.Is(err, localuser.ErrNotFound) {
			h.failPassword(ctx)

			// One answer for a code that is not valid, unknown, used or
			// expired, so the answer tells nothing about a code.
			h.writeError(ctx, w, errCodeInvalid)

			return
		}

		h.writeError(ctx, w, localUserError(err))

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// localUserActor returns the actor of a change by p, with the action
// server:manage-sign-in that each route of an administrator needs, and the
// admin flag of p. The principal ID is the
// owner of the console session of the request, and empty for a request with
// an access token: the service then refuses each change that compares it
// with the target, which fails closed.
func localUserActor(r *http.Request, p identity.Principal) localusersvc.Actor {
	id := ""
	if s, ok := sessionFromRequest(r); ok {
		id = s.Match.Owner.ID
	}

	return localusersvc.Actor{
		Entry: callerEntry(r, p, identity.ActionServerManageSignIn), PrincipalID: id, Admin: p.Admin,
	}
}

// createLocalUser makes a local user and answers 201 with its set-up code.
func (h *Handler) createLocalUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	var req createLocalUserRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	c, err := h.localUsers.CreateUser(ctx, localUserActor(r, p),
		localusersvc.CreateInput{UserName: req.UserName, DisplayName: req.DisplayName, Reason: req.Reason})
	if err != nil {
		h.writeError(ctx, w, localUserError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, createLocalUserResponse{
		PrincipalID: c.Principal.ID, UserID: c.User.ID, UserName: c.User.UserName, Subject: c.Principal.Subject,
		SetUpCode: c.SetUp.Text, SetUpExpiresAt: c.SetUp.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// getLocalUserPolicy answers the effective password policy.
func (h *Handler) getLocalUserPolicy(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	p, err := h.localUsers.Policy(ctx)
	if err != nil {
		h.writeError(ctx, w, localUserError(err))

		return
	}

	prune := localusersvc.Prune()
	rules := localusersvc.Rules()

	h.writeJSON(ctx, w, http.StatusOK, localUserPolicyResponse{
		Enabled: p.Enabled, EnabledSource: string(p.EnabledSource), IdentityProvider: p.SSO, MinLength: p.MinLength,
		MaxLength: localuser.MaxLength, RequireLettersAndDigits: p.RequireLettersAndDigits,
		ResetLifetimeSeconds: int64(p.ResetLifetime / time.Second),
		LockPrune: localUserLockPruneBody{
			IntervalSeconds: int64(prune.Interval / time.Second), AgeSeconds: int64(prune.Age / time.Second),
			MaxRows: prune.MaxRows, MaxEndedLocks: prune.MaxEnded,
		},
		LockRules: localUserLockRulesBody{
			Password: lockRuleBody(rules.Password), PasswordName: lockRuleBody(rules.PasswordName),
			SourcePrefix: localUserSourcePrefixBody{IPv4Bits: rules.IPv4PrefixBits, IPv6Bits: rules.IPv6PrefixBits},
		},
	})
}

// resetLocalUser makes a reset code and answers 201 with it.
func (h *Handler) resetLocalUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noReferrer(w)

	var req resetLocalUserRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := identity.ParsePrincipalObject(req.PrincipalID); err != nil {
		h.writeError(ctx, w, localUserError(localuser.ErrNotFound))

		return
	}

	code, err := h.localUsers.StartReset(ctx, localUserActor(r, p),
		req.PrincipalID, localusersvc.ResetInput{Reason: req.Reason, RevokeAccessTokens: req.RevokeAccessTokens})
	if err != nil {
		h.writeError(ctx, w, localUserError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, resetLocalUserResponse{
		ResetCode: code.Text, ExpiresAt: code.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// unlockLocalUser ends every count and lock of the name of a local user.
func (h *Handler) unlockLocalUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	var req unlockLocalUserRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := identity.ParsePrincipalObject(req.Principal); err != nil {
		h.writeError(ctx, w, localUserError(localuser.ErrNotFound))

		return
	}

	n, err := h.localUsers.Unlock(ctx, localUserActor(r, p), req.Principal,
		req.Reason)
	if err != nil {
		h.writeError(ctx, w, localUserError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, unlockLocalUserResponse{Cleared: n})
}

// getLocalUserLocks answers the state of each count of the name of the local
// user that the query parameter principal names.
func (h *Handler) getLocalUserLocks(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	id := queryOf(r).Get("principal")
	if _, err := identity.ParsePrincipalObject(id); err != nil {
		h.writeError(ctx, w, localUserError(localuser.ErrNotFound))

		return
	}

	locks, err := h.localUsers.Locks(ctx, localUserActor(r, p), id)
	if err != nil {
		h.writeError(ctx, w, localUserError(err))

		return
	}

	out := localUserLocksResponse{Locks: make([]localUserLockBody, 0, len(locks))}

	for _, l := range locks {
		until := ""
		if !l.LockedUntil.IsZero() {
			until = l.LockedUntil.UTC().Format(time.RFC3339)
		}

		out.Locks = append(out.Locks, localUserLockBody{
			Scope: l.Scope, Source: l.Source, LockedUntil: until, Failures: l.Failures, Level: l.Level,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// changeOwnPassword changes the password of the caller. The change ends every
// session of the person; a request with a console session gets a new session
// cookie, so the browser of the change stays signed in.
func (h *Handler) changeOwnPassword(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	caller, isSession := sessionFromRequest(r)
	if !isSession {
		// Only a person in a console session changes the own password: an
		// access token never stands for a fresh authentication or for the
		// password of a person.
		h.writeError(ctx, w, errFreshStepRequired)

		return
	}

	// The check of a current password spends the password budget of the
	// address as a sign-in does, so a stolen session cannot guess the
	// password of its person at the speed of the server.
	if err := h.admitPassword(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req changeOwnPasswordRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	fresh := req.CurrentPassword == "" && h.checkTokenFreshness(r) == nil
	actor := localusersvc.Actor{
		Entry: callerEntry(r, p, identity.ActionCallerManageSignIn), PrincipalID: caller.Match.Owner.ID,
		Admin: false,
	}

	g, err := h.localUsers.ChangePassword(ctx, actor, localusersvc.ChangeInput{
		Current: req.CurrentPassword, New: req.NewPassword, Generation: caller.Match.Session.Generation,
		Fresh: fresh,
	})
	if err != nil {
		if errors.Is(err, localuser.ErrWrongPassword) {
			h.failPassword(ctx)
		}

		h.writeError(ctx, w, localUserError(err))

		return
	}

	h.renewSession(ctx, w, r, caller, g)
	w.WriteHeader(http.StatusNoContent)
}

// renewSession starts a new session for the caller of a password change,
// with the session generation g that the change wrote, and sets its cookie.
// The sign-in time of the old session stays. The store refuses the session
// when another change bumped the generation after g. A failure leaves the
// browser signed out, which is the safe direction: the password changed.
func (h *Handler) renewSession(
	ctx context.Context, w http.ResponseWriter, r *http.Request, caller sessionCaller, g uint64,
) {
	// Defense in depth: the service returns the generation of its write and
	// never 0. This check stays so that a renewal never starts a session that
	// takes the stored generation, whatever change wrote it.
	if h.signIn == nil || g == 0 {
		return
	}

	old := caller.Match.Session
	e := signInEntry(r, caller.Match.Owner.Subject, session.ActionSignIn)

	sess, secret, err := h.signIn.sessions.Start(ctx, e, session.Start{
		AuthTime: old.AuthTime, PrincipalID: old.PrincipalID, Provider: old.Provider, SourceIP: e.SourceIP,
		UserAgent: e.UserAgent, Generation: g, Stored: false, Alert: nil,
	})
	if err != nil {
		return
	}

	http.SetCookie(w, sessionCookie(secret.Reveal(), h.sessionLength(ctx, sess)))
}

// localUserError returns the coded answer of an error of the local user
// service. An error that this table does not name passes as it is, and the
// writer answers it as an internal error with no detail.
func localUserError(err error) error {
	if minLength, ok := localusersvc.MinLengthOf(err); ok {
		return &codedError{
			status: http.StatusBadRequest, code: errcode.PasswordTooShort,
			args: []errcode.Value{errcode.Quantity("minLength", int64(minLength))}, message: shortMessage(minLength),
		}
	}

	for _, m := range localUserErrors() {
		if errors.Is(err, m.err) {
			return m.answer
		}
	}

	return err
}

// shortMessage is the text of a password below the minimum length.
func shortMessage(minLength int) string {
	return "the password is too short: use " + strconv.Itoa(minLength) + " characters or more"
}

// localUserMapping is one row of the answers of the local user errors.
type localUserMapping struct {
	err    error
	answer *codedError
}

// localUserErrors returns the answers of the local user errors, in the order
// of the check. The texts name the rule and never a password, a hash or a
// code.
func localUserErrors() []localUserMapping {
	return slices.Concat([]localUserMapping{
		{
			err: localuser.ErrSignInFailed,
			answer: &codedError{
				status:  http.StatusUnauthorized,
				code:    errcode.LocalUserSignInFailed,
				args:    nil,
				message: "the user name or the password is wrong",
			},
		},
		{
			err: localuser.ErrPasswordSignInOff,
			answer: &codedError{
				status:  http.StatusForbidden,
				code:    errcode.LocalUserPasswordSignInOff,
				args:    nil,
				message: "password sign-in is off on this installation",
			},
		},
		{
			err: localuser.ErrSecondFactorRequired,
			answer: &codedError{
				status:  http.StatusForbidden,
				code:    errcode.LocalUserSecondFactorRequired,
				args:    nil,
				message: "the password sign-in needs a second factor: sign in with the identity provider",
			},
		},
		{
			err: localuser.ErrHashBusy,
			answer: &codedError{
				status:  http.StatusTooManyRequests,
				code:    errcode.LocalUserHashBusy,
				args:    nil,
				message: "the server is busy: try again in a few seconds",
			},
		},
	}, changeErrors(), credentialErrors())
}

// changeErrors returns the answers of the refusals of a change that the
// state of the session or of the grants decides.
func changeErrors() []localUserMapping {
	return []localUserMapping{
		{localuser.ErrGenerationChanged, &codedError{
			status: http.StatusUnauthorized, code: errcode.LocalUserSessionEnded, args: nil,
			message: "the session ended because another change ended it: sign in again",
		}},
		{localuser.ErrOutsideReach, &codedError{
			status:  http.StatusForbidden,
			code:    errcode.LocalUserOutsideReach,
			args:    nil,
			message: "the request needs a reach that covers each grant of the person: ask an administrator who holds them",
		}},
	}
}

// credentialErrors returns the answers of the errors of the password rules,
// of the credential checks and of the local user records, in the order of
// the check. ErrNotFound comes last.
func credentialErrors() []localUserMapping {
	return []localUserMapping{
		{localuser.ErrPasswordTooLong, &codedError{
			status: http.StatusBadRequest, code: errcode.PasswordTooLong, args: nil,
			message: "the password is too long: use 256 characters or fewer",
		}},
		{localuser.ErrPasswordForm, &codedError{
			status: http.StatusBadRequest, code: errcode.PasswordForm, args: nil,
			message: "the password holds a character that is not allowed: remove control characters",
		}},
		{localuser.ErrPasswordLettersAndDigits, &codedError{
			status: http.StatusBadRequest, code: errcode.PasswordLettersAndDigits, args: nil,
			message: "the password needs a letter and a digit",
		}},
		{localuser.ErrPasswordBlocked, &codedError{
			status: http.StatusBadRequest, code: errcode.PasswordBlocked, args: nil,
			message: "the password is too common or holds a name: choose another password",
		}},
		{localuser.ErrCurrentPasswordRequired, &codedError{
			status: http.StatusForbidden, code: errcode.LocalUserCurrentRequired, args: nil,
			message: "the change needs the current password or a recent sign-in",
		}},
		{localuser.ErrWrongPassword, &codedError{
			status: http.StatusForbidden, code: errcode.LocalUserCurrentWrong, args: nil,
			message: "the current password is wrong",
		}},
		{localuser.ErrOwnAccount, &codedError{
			status: http.StatusForbidden, code: errcode.LocalUserOwnAccount, args: nil,
			message: "an administrator cannot reset or remove the own password: use POST /api/dustpan/v1/me/password",
		}},
		{localuser.ErrReasonRequired, &codedError{
			status: http.StatusBadRequest, code: errcode.LocalUserReasonRequired, args: nil,
			message: "the change needs a reason",
		}},
		{localuser.ErrLastCredential, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserLastCredential, args: nil,
			message: "the change removes the last sign-in credential of the person",
		}},
		{localuser.ErrBreakGlass, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserBreakGlass, args: nil,
			message: "a break-glass account signs in with a passkey or a hardware key only",
		}},
		{localuser.ErrDemoFixed, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserDemoFixed, args: nil,
			message: "the credentials of the demo user cannot change",
		}},
		{localuser.ErrAlreadyExists, &codedError{
			status: http.StatusConflict, code: errcode.LocalUserExists, args: nil,
			message: "another local user has the user name",
		}},
		{localuser.ErrInvalid, &codedError{
			status: http.StatusBadRequest, code: errcode.LocalUserNameInvalid, args: nil,
			message: "the user name is not valid: use 1 to 64 lowercase letters, digits, '.', '_' and '-'",
		}},
		{localuser.ErrUnavailable, &codedError{
			status: http.StatusServiceUnavailable, code: errcode.LocalUserUnavailable, args: nil,
			message: "the local users cannot answer: try again later",
		}},
		{principal.ErrReservedSubject, &codedError{
			status: http.StatusBadRequest, code: errcode.LocalUserNameInvalid, args: nil,
			message: "the user name is not valid",
		}},
		{localuser.ErrNotFound, &codedError{
			status: http.StatusNotFound, code: errcode.LocalUserNotFound, args: nil,
			message: "no such local user or code",
		}},
	}
}

// errCodeInvalid is the answer of a redeem whose code is not valid, used or
// expired: one text for each case.
var errCodeInvalid = &codedError{
	status: http.StatusBadRequest, code: errcode.LocalUserCodeInvalid, args: nil,
	message: "the code is not valid, was used or expired: ask an administrator for a new code",
}
